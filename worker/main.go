package main

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"math"
	"net"
	"os"
	"runtime"
	"sort"
	"sync"
	"time"

	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/mongo"
	"go.mongodb.org/mongo-driver/mongo/options"
)

const (
	PORT        = ":8081"
	MIN_RATINGS = 5
)

// UserRatings: Mapa en memoria para acceso O(1) a los ratings de un usuario
// Map[UserID] -> Map[MovieID] -> Rating
type UserRatings map[string]map[string]float64

type RatingDoc struct {
	UserID  string  `bson:"user_id"`
	MovieID string  `bson:"movie_id"`
	Score   float64 `bson:"score"`
}

type WorkerRequest struct {
	TargetUserID string `json:"target_user_id"`
}

type WorkerResponse struct {
	NodeID          string             `json:"node_id"`
	ProcessedCount  int                `json:"processed_count"`
	Recommendations []SimilarityResult `json:"recommendations"`
	Error           string             `json:"error,omitempty"`
}

type SimilarityResult struct {
	UserID string  `json:"user_id"`
	Score  float64 `json:"score"`
}

var (
	dataMutex    sync.RWMutex
	globalData   UserRatings
	allUserIDs   []string
	nodeHostname string
)

func main() {

	nodeHostname, _ = os.Hostname()
	log.Printf("Nodo ML [%s] Iniciando...", nodeHostname)

	// 1. Cargar Datos de MongoDB a Memoria
	if err := loadDataFromMongo(); err != nil {
		log.Fatalf("Error fatal cargando datos: %v", err)
	}

	// 2. Iniciar Servidor TCP
	listener, err := net.Listen("tcp", PORT)
	if err != nil {
		log.Fatalf("Error iniciando TCP: %v", err)
	}
	defer listener.Close()

	log.Printf("Nodo ML listo y escuchando en %s", PORT)
	log.Printf("Datos en memoria: %d usuarios cargados.", len(allUserIDs))

	// 3. Bucle de aceptación de tareas
	for {
		conn, err := listener.Accept()
		if err != nil {
			log.Printf("Error de conexión: %v", err)
			continue
		}

		go handleConnection(conn)
	}
}

func handleConnection(conn net.Conn) {
	defer conn.Close()
	start := time.Now()

	// 1. Decodificar la tarea
	var req WorkerRequest
	decoder := json.NewDecoder(conn)
	if err := decoder.Decode(&req); err != nil {
		log.Printf("⚠️ Error decodificando JSON: %v", err)
		return
	}

	log.Printf("Tarea recibida: Calcular para %s", req.TargetUserID)

	// 2. EJECUTAR CÁLCULO
	results, count, err := calculateSimilarityDistributed(req.TargetUserID)

	resp := WorkerResponse{
		NodeID:         nodeHostname,
		ProcessedCount: count,
	}

	if err != nil {
		resp.Error = err.Error()
	} else {
		resp.Recommendations = results
	}

	// 3. Enviar Respuesta
	encoder := json.NewEncoder(conn)
	if err := encoder.Encode(resp); err != nil {
		log.Printf("Error enviando respuesta: %v", err)
	}

	log.Printf("Tarea completada para %s en %v (Comparado con %d usuarios)", req.TargetUserID, time.Since(start), count)
}

// calculateSimilarityDistributed realiza la comparación 1-vs-N
// Utiliza paralelismo interno para aprovechar todos los núcleos del Worker.
func calculateSimilarityDistributed(targetID string) ([]SimilarityResult, int, error) {
	dataMutex.RLock()
	defer dataMutex.RUnlock()

	targetRatings, ok := globalData[targetID]
	if !ok {
		return nil, 0, fmt.Errorf("usuario objetivo %s no encontrado en este nodo", targetID)
	}

	numUsers := len(allUserIDs)
	if numUsers == 0 {
		return []SimilarityResult{}, 0, nil
	}

	// A. Configuración de Paralelismo Interno
	// Usamos el número de CPUs disponibles en este contenedor
	numWorkers := runtime.NumCPU()
	chunkSize := (numUsers + numWorkers - 1) / numWorkers

	// Canal para recolectar resultados parciales de cada goroutine interna
	resultsChan := make(chan []SimilarityResult, numWorkers)
	var wg sync.WaitGroup

	for i := 0; i < numWorkers; i++ {
		start := i * chunkSize
		end := start + chunkSize
		if end > numUsers {
			end = numUsers
		}

		wg.Add(1)
		go func(s, e int) {
			defer wg.Done()
			localResults := make([]SimilarityResult, 0, (e-s)/10)

			for idx := s; idx < e; idx++ {
				otherUserID := allUserIDs[idx]

				if otherUserID == targetID {
					continue
				}

				otherRatings := globalData[otherUserID]
				score := cosineSimilarity(targetRatings, otherRatings)

				if score > 0.1 {
					localResults = append(localResults, SimilarityResult{
						UserID: otherUserID,
						Score:  score,
					})
				}
			}
			resultsChan <- localResults
		}(start, end)
	}

	go func() {
		wg.Wait()
		close(resultsChan)
	}()

	finalResults := make([]SimilarityResult, 0, 1000)
	for chunk := range resultsChan {
		finalResults = append(finalResults, chunk...)
	}

	sort.Slice(finalResults, func(i, j int) bool {
		return finalResults[i].Score > finalResults[j].Score
	})

	topK := 50
	if len(finalResults) < topK {
		topK = len(finalResults)
	}

	return finalResults[:topK], numUsers, nil
}

func cosineSimilarity(ratingsA, ratingsB map[string]float64) float64 {

	if len(ratingsA) > len(ratingsB) {
		ratingsA, ratingsB = ratingsB, ratingsA
	}

	dotProduct := 0.0
	normA_sq := 0.0

	for movieID, scoreA := range ratingsA {
		normA_sq += scoreA * scoreA
		if scoreB, ok := ratingsB[movieID]; ok {
			dotProduct += scoreA * scoreB
		}
	}

	if dotProduct == 0 {
		return 0.0
	}

	normB_sq := 0.0
	for _, scoreB := range ratingsB {
		normB_sq += scoreB * scoreB
	}

	if normA_sq == 0 || normB_sq == 0 {
		return 0.0
	}

	return dotProduct / (math.Sqrt(normA_sq) * math.Sqrt(normB_sq))
}

//  CARGA DE DATOS (MongoDB -> RAM)

func loadDataFromMongo() error {
	log.Println("Conectando a MongoDB...")

	// URI configurada para Docker
	mongoURI := os.Getenv("MONGO_URI")
	if mongoURI == "" {
		mongoURI = "mongodb://localhost:27017"
	}

	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	client, err := mongo.Connect(ctx, options.Client().ApplyURI(mongoURI))
	if err != nil {
		return err
	}
	defer client.Disconnect(ctx)

	coll := client.Database("movielens").Collection("ratings")

	log.Println("Descargando ratings a memoria...")

	opts := options.Find().SetProjection(bson.D{
		{Key: "user_id", Value: 1},
		{Key: "movie_id", Value: 1},
		{Key: "score", Value: 1},
	})

	cursor, err := coll.Find(ctx, bson.D{}, opts)
	if err != nil {
		return err
	}
	defer cursor.Close(ctx)

	tempData := make(UserRatings)
	tempIDs := make([]string, 0, 70000)

	count := 0
	for cursor.Next(ctx) {
		var r RatingDoc
		if err := cursor.Decode(&r); err != nil {
			continue
		}

		if _, exists := tempData[r.UserID]; !exists {
			tempData[r.UserID] = make(map[string]float64)
			tempIDs = append(tempIDs, r.UserID)
		}
		tempData[r.UserID][r.MovieID] = r.Score
		count++

		if count%1000000 == 0 {
			fmt.Printf("\r   -> %d ratings procesados...", count)
		}
	}

	dataMutex.Lock()
	globalData = tempData
	allUserIDs = tempIDs
	dataMutex.Unlock()

	log.Printf("\nCarga Completa: %d ratings de %d usuarios.", count, len(allUserIDs))
	return nil
}
