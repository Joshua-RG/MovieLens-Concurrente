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
)

var (
	PORT         = ":8080"
	WORKER_ADDRS = strings.Split(os.Getenv("WORKER_ADDRS"), ",")
	REDIS_ADDR   = os.Getenv("REDIS_ADDR")
	ctx          = context.Background()
	redisClient  *redis.Client
	TOTAL_USERS  = 70000
)

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
	Title   string  `json:"title"` // [NUEVO] Recibimos el título
	Score   float64 `json:"score"`
}

type APIResponse struct {
	Source          string                `json:"source"`
	ProcessingTime  string                `json:"processing_time"`
	TotalProcessed  int                   `json:"total_processed_users"`
	FilterUsed      string                `json:"filter_used"`
	UserName        string                `json:"user_name"` // [NUEVO] Nombre del usuario
	Recommendations []MovieRecommendation `json:"recommendations"`
}

func main() {
	if len(WORKER_ADDRS) == 0 || WORKER_ADDRS[0] == "" {
		WORKER_ADDRS = []string{"localhost:8081"}
	}

	redisClient = redis.NewClient(&redis.Options{Addr: REDIS_ADDR})

	http.HandleFunc("/recommend", handleRecommend)
	log.Printf("🌐 API Coordinador lista. Workers: %v", WORKER_ADDRS)
	http.ListenAndServe(PORT, nil)
}

func handleRecommend(w http.ResponseWriter, r *http.Request) {
	start := time.Now()
	userID := r.URL.Query().Get("user_id")
	genre := r.URL.Query().Get("genre")

	// Generar nombre ficticio para el usuario
	fakeName := generateFakeName(userID)

	cacheKey := "rec:" + userID + ":" + genre

	val, err := redisClient.Get(ctx, cacheKey).Result()
	if err == nil {
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(val))
		return
	}

	numWorkers := len(WORKER_ADDRS)
	chunkSize := TOTAL_USERS / numWorkers

	var wg sync.WaitGroup
	resultsChan := make(chan WorkerResponse, numWorkers)

	for i, addr := range WORKER_ADDRS {
		wg.Add(1)
		rStart := i * chunkSize
		rEnd := rStart + chunkSize
		if i == numWorkers-1 {
			rEnd = TOTAL_USERS + 1000
		}

		go func(address string, start, end int) {
			defer wg.Done()
			resp, err := callWorker(address, userID, start, end, genre)
			if err == nil {
				resultsChan <- *resp
			}
		}(addr, rStart, rEnd)
	}

	wg.Wait()
	close(resultsChan)

	movieScores := make(map[string]float64)
	movieTitles := make(map[string]string) // Mapa temporal para guardar títulos
	totalProc := 0

	for resp := range resultsChan {
		totalProc += resp.ProcessedCount
		for _, rec := range resp.Recommendations {
			movieScores[rec.MovieID] += rec.Score
			// Guardamos el título (todos los workers mandan el mismo título para el mismo ID)
			if rec.Title != "" {
				movieTitles[rec.MovieID] = rec.Title
			}
		}
	}

	var finalRecs []MovieRecommendation
	for mID, score := range movieScores {
		finalRecs = append(finalRecs, MovieRecommendation{
			MovieID: mID,
			Title:   movieTitles[mID], // Recuperamos título
			Score:   score,
		})
	}

	sort.Slice(finalRecs, func(i, j int) bool {
		return finalRecs[i].Score > finalRecs[j].Score
	})
	if len(finalRecs) > 10 {
		finalRecs = finalRecs[:10]
	}

	respObj := APIResponse{
		Source:          "Distributed Cluster",
		ProcessingTime:  time.Since(start).String(),
		TotalProcessed:  totalProc,
		FilterUsed:      genre,
		UserName:        fakeName, // [NUEVO] Enviamos nombre
		Recommendations: finalRecs,
	}

	jsonBytes, _ := json.Marshal(respObj)
	redisClient.Set(ctx, cacheKey, jsonBytes, 10*time.Minute)

	w.Header().Set("Content-Type", "application/json")
	w.Write(jsonBytes)
}

func callWorker(addr, userID string, start, end int, genre string) (*WorkerResponse, error) {
	conn, err := net.DialTimeout("tcp", addr, 2*time.Second)
	if err != nil {
		return nil, err
	}
	defer conn.Close()

	req := WorkerRequest{
		TargetUserID: userID,
		RangeStart:   start,
		RangeEnd:     end,
		GenreFilter:  genre,
	}
	json.NewEncoder(conn).Encode(req)

	var resp WorkerResponse
	json.NewDecoder(conn).Decode(&resp)
	return &resp, nil
}

// --- GENERADOR DE NOMBRES ALEATORIOS (Determinista) ---
func generateFakeName(userID string) string {
	adjectives := []string{"Happy", "Lucky", "Sunny", "Clever", "Brave", "Calm", "Eager", "Fancy", "Jolly", "Kind"}
	animals := []string{"Panda", "Tiger", "Eagle", "Lion", "Dolphin", "Fox", "Wolf", "Hawk", "Bear", "Owl"}

	// Usar hash del UserID para elegir siempre el mismo nombre para el mismo ID
	h := fnv.New32a()
	h.Write([]byte(userID))
	hashVal := h.Sum32()

	adjIndex := int(hashVal) % len(adjectives)
	aniIndex := int(hashVal) % len(animals)

	// Si el ID es numérico, añadimos el ID al final para que se vea único
	// Ejemplo: "Happy Panda #123"
	if _, err := strconv.Atoi(userID); err == nil {
		return fmt.Sprintf("%s %s #%s", adjectives[adjIndex], animals[aniIndex], userID)
	}

	return fmt.Sprintf("%s %s", adjectives[adjIndex], animals[aniIndex])
}
