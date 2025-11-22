package main

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"net"
	"net/http"
	"os"
	"time"

	"github.com/redis/go-redis/v9"
)

var (
	PORT        = ":8080"
	WORKER_ADDR = os.Getenv("WORKER_ADDR") // Dirección del Cluster de Workers
	REDIS_ADDR  = os.Getenv("REDIS_ADDR")  // Dirección de Redis
	ctx         = context.Background()
	redisClient *redis.Client
)

func init() {

	if WORKER_ADDR == "" {
		WORKER_ADDR = "localhost:8081"
	}
	if REDIS_ADDR == "" {
		REDIS_ADDR = "localhost:6379"
	}
}

type APIRequest struct {
	UserID string `json:"user_id"`
}

type APIResponse struct {
	Source          string             `json:"source"`
	ProcessingTime  string             `json:"processing_time"`
	Recommendations []SimilarityResult `json:"recommendations"`
}

type SimilarityResult struct {
	UserID string  `json:"user_id"`
	Score  float64 `json:"score"`
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

func main() {
	// 1. Inicializar Redis
	initRedis()

	// 2. Definir Rutas HTTP
	http.HandleFunc("/recommend", handleRecommend)

	http.HandleFunc("/health", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		w.Write([]byte("OK"))
	})

	// 3. Iniciar Servidor
	log.Printf("API Coordinador escuchando en %s", PORT)
	log.Printf("Conectando a Workers en: %s", WORKER_ADDR)
	log.Printf("Conectando a Redis en: %s", REDIS_ADDR)

	if err := http.ListenAndServe(PORT, nil); err != nil {
		log.Fatalf("Error fatal en API: %v", err)
	}
}

func handleRecommend(w http.ResponseWriter, r *http.Request) {

	w.Header().Set("Access-Control-Allow-Origin", "*")
	w.Header().Set("Content-Type", "application/json")

	start := time.Now()

	userID := r.URL.Query().Get("user_id")
	if userID == "" {
		http.Error(w, `{"error": "user_id parameter is required"}`, http.StatusBadRequest)
		return
	}

	log.Printf("Petición recibida para Usuario: %s", userID)

	// 2. Consultar CACHÉ (Redis)
	cachedVal, err := redisClient.Get(ctx, "rec:"+userID).Result()
	if err == nil {

		log.Printf("Cache HIT para %s", userID)
		w.Write([]byte(cachedVal))
		return
	}

	// 3. Si no hay caché (CACHE MISS), llamar al Cluster de Workers
	log.Printf("Cache MISS para %s. Enviando tarea al Cluster...", userID)

	workerResp, err := callWorkerCluster(userID)
	if err != nil {
		log.Printf("Error en Cluster: %v", err)
		http.Error(w, fmt.Sprintf(`{"error": "Calculation failed: %v"}`, err), http.StatusInternalServerError)
		return
	}

	// 4. Construir Respuesta Final
	apiResp := APIResponse{
		Source:          fmt.Sprintf("worker-cluster (Node: %s)", workerResp.NodeID),
		ProcessingTime:  time.Since(start).String(),
		Recommendations: workerResp.Recommendations,
	}

	// 5. Guardar en Redis (TTL 10 minutos) y Responder
	jsonBytes, _ := json.Marshal(apiResp)

	// Guardar asíncronamente para no bloquear la respuesta
	go func() {
		err := redisClient.Set(ctx, "rec:"+userID, jsonBytes, 10*time.Minute).Err()
		if err != nil {
			log.Printf("Error guardando en Redis: %v", err)
		}
	}()

	w.Write(jsonBytes)
}

func callWorkerCluster(targetID string) (*WorkerResponse, error) {

	conn, err := net.DialTimeout("tcp", WORKER_ADDR, 5*time.Second)
	if err != nil {
		return nil, fmt.Errorf("no se pudo conectar con ningún worker: %v", err)
	}
	defer conn.Close()

	req := WorkerRequest{TargetUserID: targetID}
	encoder := json.NewEncoder(conn)
	if err := encoder.Encode(req); err != nil {
		return nil, fmt.Errorf("error enviando tarea: %v", err)
	}

	conn.SetReadDeadline(time.Now().Add(30 * time.Second))

	var resp WorkerResponse
	decoder := json.NewDecoder(conn)
	if err := decoder.Decode(&resp); err != nil {
		return nil, fmt.Errorf("error recibiendo respuesta del worker: %v", err)
	}

	if resp.Error != "" {
		return nil, fmt.Errorf("worker reportó error: %s", resp.Error)
	}

	return &resp, nil
}

func initRedis() {
	redisClient = redis.NewClient(&redis.Options{
		Addr:     REDIS_ADDR,
		Password: "",
		DB:       0,
	})

	_, err := redisClient.Ping(ctx).Result()
	if err != nil {
		log.Printf("Advertencia: No se pudo conectar a Redis en %s. La caché no funcionará. (%v)", REDIS_ADDR, err)
	} else {
		log.Println("Redis conectado y listo.")
	}
}
