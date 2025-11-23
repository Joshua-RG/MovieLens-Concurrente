package main

import (
	"context"
	"encoding/json"
	"log"
	"math"
	"net"
	"os"
	"runtime"
	"sort"
	"strings"
	"sync"

	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/mongo"
	"go.mongodb.org/mongo-driver/mongo/options"
)

const PORT = ":8081"

// --- ESTRUCTURAS ---
type WorkerRequest struct {
	TargetUserID string `json:"target_user_id"`
	RangeStart   int    `json:"range_start"`
	RangeEnd     int    `json:"range_end"`
	GenreFilter  string `json:"genre_filter"`
}

type WorkerResponse struct {
	NodeID          string                `json:"node_id"`
	ProcessedCount  int                   `json:"processed_count"`
	Recommendations []MovieRecommendation `json:"recommendations"`
	Error           string                `json:"error,omitempty"`
}

type MovieRecommendation struct {
	MovieID string  `json:"movie_id"`
	Score   float64 `json:"score"`
}

type RatingDoc struct {
	UserID  string  `bson:"user_id"`
	MovieID string  `bson:"movie_id"`
	Score   float64 `bson:"score"`
}

type MovieDoc struct {
	ID     string `bson:"_id"`
	Genres string `bson:"genres"`
}

// --- VARIABLES GLOBALES ---
var (
	dataMutex   sync.RWMutex
	userRatings map[string]map[string]float64
	userNorms   map[string]float64
	movieGenres map[string]string
	allUserIDs  []string
	nodeID      string
)

func main() {
	nodeID = os.Getenv("NODE_ID")
	if nodeID == "" {
		nodeID, _ = os.Hostname()
	}
	log.Printf("👷 Nodo ML #%s Iniciando...", nodeID)

	// 1. PREPROCESAMIENTO (Carga Ratings + Películas + Normas)
	if err := loadAndPreprocess(); err != nil {
		log.Fatalf("❌ Error en preprocesamiento: %v", err)
	}

	// 2. INICIO SERVIDOR TCP
	listener, err := net.Listen("tcp", PORT)
	if err != nil {
		log.Fatalf("❌ Error TCP: %v", err)
	}
	defer listener.Close()

	log.Printf("🚀 Nodo #%s LISTO. Escuchando en %s", nodeID, PORT)

	for {
		conn, err := listener.Accept()
		if err != nil {
			continue
		}
		go handleConnection(conn)
	}
}

func handleConnection(conn net.Conn) {
	defer conn.Close()

	var req WorkerRequest
	if err := json.NewDecoder(conn).Decode(&req); err != nil {
		return
	}

	// Ejecutar cálculo paralelo optimizado
	results, count := calculateRangeParallel(req.TargetUserID, req.RangeStart, req.RangeEnd, req.GenreFilter)

	resp := WorkerResponse{
		NodeID:          nodeID,
		ProcessedCount:  count,
		Recommendations: results,
	}

	json.NewEncoder(conn).Encode(resp)
}

// --- LÓGICA DE CÁLCULO PARALELO (OPTIMIZADA PC3) ---
func calculateRangeParallel(targetID string, start, end int, genreFilter string) ([]MovieRecommendation, int) {
	dataMutex.RLock()
	defer dataMutex.RUnlock()

	targetMovies, ok := userRatings[targetID]
	if !ok {
		return nil, 0
	}
	targetNorm := userNorms[targetID]

	// Validar rangos globales
	if start < 0 {
		start = 0
	}
	if end > len(allUserIDs) {
		end = len(allUserIDs)
	}

	totalUsersInRange := end - start
	if totalUsersInRange <= 0 {
		return nil, 0
	}

	// [OPTIMIZACIÓN PC3] Definir número de Goroutines internas
	// Usamos 8 como límite superior para evitar contención de memoria
	const OPTIMAL_WORKERS_PC3 = 8
	numInternalWorkers := runtime.NumCPU()
	if numInternalWorkers > OPTIMAL_WORKERS_PC3 {
		numInternalWorkers = OPTIMAL_WORKERS_PC3
	}

	// Canales para Map-Reduce interno
	// Cada goroutine devuelve un MAPA de películas candidatas (MovieID -> Score)
	resultsChan := make(chan map[string]float64, numInternalWorkers)
	var wg sync.WaitGroup

	// Dividir el rango asignado en sub-rangos para las goroutines
	chunkSize := (totalUsersInRange + numInternalWorkers - 1) / numInternalWorkers

	for i := 0; i < numInternalWorkers; i++ {
		wStart := start + (i * chunkSize)
		wEnd := wStart + chunkSize
		if wEnd > end {
			wEnd = end
		}
		if wStart >= wEnd {
			continue
		}

		wg.Add(1)
		go func(ws, we int) {
			defer wg.Done()
			localCandidates := make(map[string]float64)

			for idx := ws; idx < we; idx++ {
				otherID := allUserIDs[idx]
				if otherID == targetID {
					continue
				}

				// Lógica de Coseno
				otherMovies := userRatings[otherID]
				otherNorm := userNorms[otherID]

				dotProduct := 0.0
				// Optimización: Iterar sobre el vector más pequeño
				if len(targetMovies) < len(otherMovies) {
					for mID, scoreA := range targetMovies {
						if scoreB, ok := otherMovies[mID]; ok {
							dotProduct += scoreA * scoreB
						}
					}
				} else {
					for mID, scoreB := range otherMovies {
						if scoreA, ok := targetMovies[mID]; ok {
							dotProduct += scoreA * scoreB
						}
					}
				}

				if dotProduct > 0 {
					similarity := dotProduct / (targetNorm * otherNorm)

					if similarity > 0.3 { // Umbral
						for mID, rating := range otherMovies {
							if _, seen := targetMovies[mID]; !seen {
								// Filtro de Género
								if genreFilter != "" && !strings.Contains(movieGenres[mID], genreFilter) {
									continue
								}
								localCandidates[mID] += similarity * rating
							}
						}
					}
				}
			}
			resultsChan <- localCandidates
		}(wStart, wEnd)
	}

	// Cerrar canal al terminar
	go func() {
		wg.Wait()
		close(resultsChan)
	}()

	// [REDUCE] Unificar mapas de candidatos
	finalCandidates := make(map[string]float64)
	for partialMap := range resultsChan {
		for mID, score := range partialMap {
			finalCandidates[mID] += score
		}
	}

	// Convertir a lista y ordenar
	var results []MovieRecommendation
	for mID, score := range finalCandidates {
		results = append(results, MovieRecommendation{MovieID: mID, Score: score})
	}

	sort.Slice(results, func(i, j int) bool { return results[i].Score > results[j].Score })
	if len(results) > 50 {
		results = results[:50]
	}

	return results, totalUsersInRange
}

// --- CARGA Y PREPROCESAMIENTO ---
func loadAndPreprocess() error {
	log.Println("⏳ [FASE 1] Cargando datos de MongoDB...")
	mongoURI := os.Getenv("MONGO_URI")
	if mongoURI == "" {
		mongoURI = "mongodb://localhost:27017"
	}

	ctx := context.TODO()
	client, _ := mongo.Connect(ctx, options.Client().ApplyURI(mongoURI))
	defer client.Disconnect(ctx)

	db := client.Database("movielens")

	// Cargar Géneros
	log.Println("   -> Cargando catálogo de películas...")
	cursorMov, err := db.Collection("movies").Find(ctx, bson.D{})
	if err != nil {
		return err
	}

	tempGenres := make(map[string]string)
	for cursorMov.Next(ctx) {
		var m MovieDoc
		cursorMov.Decode(&m)
		tempGenres[m.ID] = m.Genres
	}
	cursorMov.Close(ctx)

	// Cargar Ratings
	log.Println("   -> Cargando ratings...")
	cursor, err := db.Collection("ratings").Find(ctx, bson.D{})
	if err != nil {
		return err
	}

	tempRatings := make(map[string]map[string]float64)
	tempNorms := make(map[string]float64)
	var tempIDs []string

	count := 0
	for cursor.Next(ctx) {
		var r RatingDoc
		cursor.Decode(&r)
		if _, ok := tempRatings[r.UserID]; !ok {
			tempRatings[r.UserID] = make(map[string]float64)
			tempIDs = append(tempIDs, r.UserID)
		}
		tempRatings[r.UserID][r.MovieID] = r.Score
		count++
	}

	log.Printf("⏳ [FASE 2] Preprocesando Normas Vectoriales (Aprendizaje)...")
	for uid, movies := range tempRatings {
		sumSq := 0.0
		for _, score := range movies {
			sumSq += score * score
		}
		tempNorms[uid] = math.Sqrt(sumSq)
	}

	sort.Strings(tempIDs)

	dataMutex.Lock()
	userRatings = tempRatings
	userNorms = tempNorms
	movieGenres = tempGenres
	allUserIDs = tempIDs
	dataMutex.Unlock()

	log.Printf("✅ Preprocesamiento finalizado: %d usuarios, %d peliculas, %d ratings.", len(tempIDs), len(tempGenres), count)
	return nil
}
