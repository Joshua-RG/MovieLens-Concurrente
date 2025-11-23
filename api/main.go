package main

import (
	"context"
	"encoding/json"
	"fmt"
	"hash/fnv"
	"log"
	"net"
	"net/http"
	"os"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/redis/go-redis/v9"
	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/mongo"
	"go.mongodb.org/mongo-driver/mongo/options"
)

var (
	PORT         = ":8080"
	WORKER_ADDRS = strings.Split(os.Getenv("WORKER_ADDRS"), ",")
	REDIS_ADDR   = os.Getenv("REDIS_ADDR")
	MONGO_URI    = os.Getenv("MONGO_URI")

	ctx         = context.Background()
	redisClient *redis.Client
	mongoClient *mongo.Client
	TOTAL_USERS = 70000
)

// --- ESTRUCTURAS ---

type Movie struct {
	ID     string `json:"id" bson:"_id"`
	Title  string `json:"title" bson:"title"`
	Genres string `json:"genres" bson:"genres"`
}

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
	Title   string  `json:"title"`
	Score   float64 `json:"score"`
}

type APIResponse struct {
	Source          string                `json:"source"`
	ProcessingTime  string                `json:"processing_time"`
	TotalProcessed  int                   `json:"total_processed_users,omitempty"`
	FilterUsed      string                `json:"filter_used"`
	UserName        string                `json:"user_name"`
	NodesInvolved   []string              `json:"nodes_involved,omitempty"` // [IMPORTANTE] Para ver los nodos
	Recommendations []MovieRecommendation `json:"recommendations"`
}

func main() {
	if len(WORKER_ADDRS) == 0 || WORKER_ADDRS[0] == "" {
		WORKER_ADDRS = []string{"localhost:8081"}
	}

	redisClient = redis.NewClient(&redis.Options{Addr: REDIS_ADDR})

	if MONGO_URI == "" {
		MONGO_URI = "mongodb://localhost:27017"
	}
	var err error
	mongoClient, err = mongo.Connect(ctx, options.Client().ApplyURI(MONGO_URI))
	if err != nil {
		log.Fatalf("❌ Error conectando a Mongo: %v", err)
	}

	http.HandleFunc("/recommend", handleRecommend)
	http.HandleFunc("/movies", handleGetMovies)

	log.Printf("🌐 API Coordinador lista en %s", PORT)
	log.Printf("🔗 Workers detectados: %v", WORKER_ADDRS)
	http.ListenAndServe(PORT, nil)
}

func handleGetMovies(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Access-Control-Allow-Origin", "*")
	w.Header().Set("Content-Type", "application/json")
	limit, _ := strconv.ParseInt(r.URL.Query().Get("limit"), 10, 64)
	skip, _ := strconv.ParseInt(r.URL.Query().Get("skip"), 10, 64)
	if limit == 0 {
		limit = 20
	}

	coll := mongoClient.Database("movielens").Collection("movies")
	opts := options.Find().SetLimit(limit).SetSkip(skip)
	cursor, _ := coll.Find(ctx, bson.D{}, opts)
	var movies []Movie
	cursor.All(ctx, &movies)
	json.NewEncoder(w).Encode(map[string]interface{}{"count": len(movies), "movies": movies})
}

func handleRecommend(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	start := time.Now()
	userID := r.URL.Query().Get("user_id")
	genre := r.URL.Query().Get("genre")
	fakeName := generateFakeName(userID)
	cacheKey := "rec:" + userID + ":" + genre

	// 1. INTENTO DE CACHÉ (Optimizado)
	val, err := redisClient.Get(ctx, cacheKey).Result()
	if err == nil {
		// ¡CACHE HIT!
		// Deserializamos SOLO la lista de películas guardada
		var cachedRecs []MovieRecommendation
		json.Unmarshal([]byte(val), &cachedRecs)

		// Construimos una respuesta FRESCA con el tiempo actual (super rápido)
		respObj := APIResponse{
			Source:          "Cache (Redis)",            // <--- AQUI SE VE EL CAMBIO
			ProcessingTime:  time.Since(start).String(), // <--- TIEMPO REAL (Microsegundos)
			FilterUsed:      genre,
			UserName:        fakeName,
			Recommendations: cachedRecs,
		}
		log.Printf("✅ [CACHE HIT] Respuesta rápida para Usuario %s", userID)
		json.NewEncoder(w).Encode(respObj)
		return
	}

	// 2. SI NO HAY CACHÉ -> CLUSTER DE WORKERS
	log.Printf("⚠️ [CACHE MISS] Usuario %s. Iniciando Scatter-Gather a %d nodos...", userID, len(WORKER_ADDRS))

	numWorkers := len(WORKER_ADDRS)
	chunkSize := TOTAL_USERS / numWorkers

	var wg sync.WaitGroup
	resultsChan := make(chan WorkerResponse, numWorkers)

	// SCATTER (Distribuir)
	for i, addr := range WORKER_ADDRS {
		wg.Add(1)
		rStart := i * chunkSize
		rEnd := rStart + chunkSize
		if i == numWorkers-1 {
			rEnd = TOTAL_USERS + 1000
		}

		go func(address string, start, end int) {
			defer wg.Done()
			log.Printf(" -> Enviando tarea a %s (Rango %d-%d)", address, start, end) // LOG PARA EVIDENCIA

			resp, err := callWorker(address, userID, start, end, genre)
			if err == nil {
				log.Printf(" <- Recibido de %s (NodoID: %s)", address, resp.NodeID) // LOG PARA EVIDENCIA
				resultsChan <- *resp
			} else {
				log.Printf("❌ Error en nodo %s: %v", address, err)
			}
		}(addr, rStart, rEnd)
	}

	wg.Wait()
	close(resultsChan)

	// GATHER (Recolectar)
	movieScores := make(map[string]float64)
	movieTitles := make(map[string]string)
	totalProc := 0
	var nodesInvolved []string // Lista para guardar los IDs de los nodos

	for resp := range resultsChan {
		totalProc += resp.ProcessedCount
		nodesInvolved = append(nodesInvolved, resp.NodeID) // Guardamos quién respondió

		for _, rec := range resp.Recommendations {
			movieScores[rec.MovieID] += rec.Score
			if rec.Title != "" {
				movieTitles[rec.MovieID] = rec.Title
			}
		}
	}

	// Ordenar
	var finalRecs []MovieRecommendation
	for mID, score := range movieScores {
		finalRecs = append(finalRecs, MovieRecommendation{
			MovieID: mID,
			Title:   movieTitles[mID],
			Score:   score,
		})
	}
	sort.Slice(finalRecs, func(i, j int) bool { return finalRecs[i].Score > finalRecs[j].Score })
	if len(finalRecs) > 10 {
		finalRecs = finalRecs[:10]
	}

	// Respuesta Final (Cluster)
	respObj := APIResponse{
		Source:          "Distributed Cluster (Scatter-Gather)",
		ProcessingTime:  time.Since(start).String(),
		TotalProcessed:  totalProc,
		FilterUsed:      genre,
		UserName:        fakeName,
		NodesInvolved:   nodesInvolved, // <--- AQUI SE VEN LOS NODOS
		Recommendations: finalRecs,
	}

	// GUARDAR EN CACHÉ (Solo la lista de películas, para ahorrar espacio y permitir metadata dinámica)
	recsBytes, _ := json.Marshal(finalRecs)
	redisClient.Set(ctx, cacheKey, recsBytes, 10*time.Minute)

	log.Printf("🔄 [FIN] Procesado en %v. Nodos: %v", time.Since(start), nodesInvolved)
	json.NewEncoder(w).Encode(respObj)
}

func callWorker(addr, userID string, start, end int, genre string) (*WorkerResponse, error) {
	conn, err := net.DialTimeout("tcp", addr, 2*time.Second)
	if err != nil {
		return nil, err
	}
	defer conn.Close()

	req := WorkerRequest{
		TargetUserID: userID, RangeStart: start, RangeEnd: end, GenreFilter: genre,
	}
	json.NewEncoder(conn).Encode(req)

	var resp WorkerResponse
	json.NewDecoder(conn).Decode(&resp)
	return &resp, nil
}

func generateFakeName(userID string) string {
	adjectives := []string{"Happy", "Lucky", "Sunny", "Clever", "Brave", "Calm", "Eager", "Fancy", "Jolly", "Kind"}
	animals := []string{"Panda", "Tiger", "Eagle", "Lion", "Dolphin", "Fox", "Wolf", "Hawk", "Bear", "Owl"}
	h := fnv.New32a()
	h.Write([]byte(userID))
	hashVal := h.Sum32()
	adjIndex := int(hashVal) % len(adjectives)
	aniIndex := int(hashVal) % len(animals)
	if _, err := strconv.Atoi(userID); err == nil {
		return fmt.Sprintf("%s %s #%s", adjectives[adjIndex], animals[aniIndex], userID)
	}
	return fmt.Sprintf("%s %s", adjectives[adjIndex], animals[aniIndex])
}
