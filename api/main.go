package main

import (
	"context"
	"encoding/json"
	"log"
	"net"
	"net/http"
	"os"
	"sort"
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
	GenreFilter  string `json:"genre_filter"` // [NUEVO]
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

type APIResponse struct {
	Source          string                `json:"source"`
	ProcessingTime  string                `json:"processing_time"`
	TotalProcessed  int                   `json:"total_processed_users"`
	FilterUsed      string                `json:"filter_used"` // [NUEVO]
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
	genre := r.URL.Query().Get("genre") // [NUEVO] Leer género

	// [IMPORTANTE] Clave de Caché debe incluir el género
	cacheKey := "rec:" + userID + ":" + genre

	// 1. Check Caché
	val, err := redisClient.Get(ctx, cacheKey).Result()
	if err == nil {
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(val))
		return
	}

	// 2. Scatter (Distribuir)
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
			// [CAMBIO] Pasamos 'genre' al worker
			resp, err := callWorker(address, userID, start, end, genre)
			if err == nil {
				resultsChan <- *resp
			}
		}(addr, rStart, rEnd)
	}

	wg.Wait()
	close(resultsChan)

	// 3. Gather (Sumar puntajes de películas)
	movieScores := make(map[string]float64)
	totalProc := 0

	for resp := range resultsChan {
		totalProc += resp.ProcessedCount
		// Sumar puntajes parciales de cada worker
		for _, rec := range resp.Recommendations {
			movieScores[rec.MovieID] += rec.Score
		}
	}

	// Convertir a lista y ordenar
	var finalRecs []MovieRecommendation
	for mID, score := range movieScores {
		finalRecs = append(finalRecs, MovieRecommendation{MovieID: mID, Score: score})
	}

	sort.Slice(finalRecs, func(i, j int) bool {
		return finalRecs[i].Score > finalRecs[j].Score
	})
	if len(finalRecs) > 10 {
		finalRecs = finalRecs[:10]
	}

	// Respuesta final
	respObj := APIResponse{
		Source:          "Distributed Cluster",
		ProcessingTime:  time.Since(start).String(),
		TotalProcessed:  totalProc,
		FilterUsed:      genre,
		Recommendations: finalRecs,
	}

	jsonBytes, _ := json.Marshal(respObj)

	// Guardar en Redis
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
		GenreFilter:  genre, // [NUEVO]
	}
	json.NewEncoder(conn).Encode(req)

	var resp WorkerResponse
	json.NewDecoder(conn).Decode(&resp)
	return &resp, nil
}
