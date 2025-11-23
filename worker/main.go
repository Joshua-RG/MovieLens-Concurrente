package main

import (
	"context"
	"encoding/json"
	"log"
	"math"
	"net"
	"os"
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
	GenreFilter  string `json:"genre_filter"` // [NUEVO] Parámetro de Género
}

type WorkerResponse struct {
	NodeID          string                `json:"node_id"`
	ProcessedCount  int                   `json:"processed_count"`
	Recommendations []MovieRecommendation `json:"recommendations"` // [CAMBIO] Devolvemos Películas
	Error           string                `json:"error,omitempty"`
}

type MovieRecommendation struct { // [NUEVO] Estructura para películas
	MovieID string  `json:"movie_id"`
	Score   float64 `json:"score"`
}

type RatingDoc struct {
	UserID  string  `bson:"user_id"`
	MovieID string  `bson:"movie_id"`
	Score   float64 `bson:"score"`
}

type MovieDoc struct { // [NUEVO] Para cargar géneros
	ID     string `bson:"_id"`
	Genres string `bson:"genres"`
}

// --- VARIABLES GLOBALES ---
var (
	dataMutex   sync.RWMutex
	userRatings map[string]map[string]float64
	userNorms   map[string]float64
	movieGenres map[string]string // [NUEVO] Mapa de ID -> Géneros (RAM)
	allUserIDs  []string
	nodeID      string
)

func main() {
	nodeID = os.Getenv("NODE_ID")
	if nodeID == "" {
		nodeID, _ = os.Hostname()
	}
	log.Printf("👷 Nodo ML #%s Iniciando...", nodeID)

	// 1. PREPROCESAMIENTO (Carga Ratings + Películas)
	if err := loadAndPreprocess(); err != nil {
		log.Fatalf("❌ Error en preprocesamiento: %v", err)
	}

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

	// [CAMBIO] Pasamos el filtro de género al cálculo
	results, count := calculateRange(req.TargetUserID, req.RangeStart, req.RangeEnd, req.GenreFilter)

	resp := WorkerResponse{
		NodeID:          nodeID,
		ProcessedCount:  count,
		Recommendations: results,
	}

	json.NewEncoder(conn).Encode(resp)
}

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

	// [NUEVO] Cargar Géneros de Películas
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

	// Cargar Ratings (Igual que antes)
	cursor, err := db.Collection("ratings").Find(ctx, bson.D{})
	if err != nil {
		return err
	}

	tempRatings := make(map[string]map[string]float64)
	tempNorms := make(map[string]float64)
	var tempIDs []string

	for cursor.Next(ctx) {
		var r RatingDoc
		cursor.Decode(&r)
		if _, ok := tempRatings[r.UserID]; !ok {
			tempRatings[r.UserID] = make(map[string]float64)
			tempIDs = append(tempIDs, r.UserID)
		}
		tempRatings[r.UserID][r.MovieID] = r.Score
	}

	// Cálculo de Normas (Igual que antes)
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
	movieGenres = tempGenres // [NUEVO] Guardar géneros
	allUserIDs = tempIDs
	dataMutex.Unlock()

	log.Printf("✅ Preprocesamiento finalizado: %d usuarios y %d películas.", len(tempIDs), len(tempGenres))
	return nil
}

// --- CÁLCULO FINAL (Similitud + Filtro de Género) ---
func calculateRange(targetID string, start, end int, genreFilter string) ([]MovieRecommendation, int) {
	dataMutex.RLock()
	defer dataMutex.RUnlock()

	targetMovies, ok := userRatings[targetID]
	if !ok {
		return nil, 0
	}
	targetNorm := userNorms[targetID]

	if start < 0 {
		start = 0
	}
	if end > len(allUserIDs) {
		end = len(allUserIDs)
	}

	// Mapa para acumular puntajes de películas candidatas
	candidateMovies := make(map[string]float64)
	processed := 0

	// 1. Encontrar Vecinos (Collaborative Filtering)
	for i := start; i < end; i++ {
		otherID := allUserIDs[i]
		if otherID == targetID {
			continue
		}

		otherMovies := userRatings[otherID]
		otherNorm := userNorms[otherID]

		dotProduct := 0.0
		for mID, scoreA := range targetMovies {
			if scoreB, ok := otherMovies[mID]; ok {
				dotProduct += scoreA * scoreB
			}
		}

		if dotProduct > 0 {
			similarity := dotProduct / (targetNorm * otherNorm)

			// Si es un vecino similar, agregamos sus películas a los candidatos
			if similarity > 0.3 { // Umbral de similitud
				for mID, rating := range otherMovies {
					// Solo recomendar si el target NO la ha visto
					if _, seen := targetMovies[mID]; !seen {
						// [NUEVO] FILTRO DE GÉNERO
						// Si hay filtro y la película no lo contiene, saltar.
						if genreFilter != "" && !strings.Contains(movieGenres[mID], genreFilter) {
							continue
						}

						// Score = Similitud del vecino * Rating que le dio
						candidateMovies[mID] += similarity * rating
					}
				}
			}
		}
		processed++
	}

	// 2. Convertir mapa a lista para ordenar
	var results []MovieRecommendation
	for mID, score := range candidateMovies {
		results = append(results, MovieRecommendation{MovieID: mID, Score: score})
	}

	// 3. Ordenar Top Películas
	sort.Slice(results, func(i, j int) bool { return results[i].Score > results[j].Score })
	if len(results) > 50 {
		results = results[:50]
	}

	return results, processed
}
