package main

import (
	"context"
	"encoding/json"
	"fmt"
	"hash/fnv"
	"io"
	"log"
	"net"
	"net/http"
	"os"
	"runtime"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/docker/docker/api/types"
	"github.com/docker/docker/client"
	"github.com/golang-jwt/jwt/v5"
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
	JWT_SECRET   = []byte("dark_souls_3_peak")

	ctx         = context.Background()
	redisClient *redis.Client
	mongoClient *mongo.Client
	TOTAL_USERS = 163000
)

type RegisterRequest struct {
	Username string `json:"username"`
	Password string `json:"password"`
}

type RateRequest struct {
	UserID  string  `json:"user_id"`
	MovieID string  `json:"movie_id"`
	Score   float64 `json:"score"`
}

type LoginRequest struct {
	UserID   string `json:"user_id"`
	Password string `json:"password"`
}

type LoginResponse struct {
	Token    string `json:"token"`
	UserName string `json:"user_name"`
}

type UserClaims struct {
	UserID string `json:"user_id"`
	jwt.RegisteredClaims
}

type ClusterStats struct {
	ActiveWorkers int    `json:"active_workers"`
	CPUNum        int    `json:"cpu_cores_api"`
	Goroutines    int    `json:"goroutines_api"`
	MemoryUsage   uint64 `json:"memory_usage_mb"`
}

type Movie struct {
	ID     string `json:"id" bson:"_id"`
	Title  string `json:"title" bson:"title"`
	Genres string `json:"genres" bson:"genres"`
}

type RatingHistory struct {
	MovieID string  `json:"movie_id" bson:"movie_id"`
	Title   string  `json:"title,omitempty"`
	Score   float64 `json:"score" bson:"score"`
}

type WorkerRequest struct {
	OpType       string  `json:"op_type"`
	TargetUserID string  `json:"target_user_id"`
	RangeStart   int     `json:"range_start"`
	RangeEnd     int     `json:"range_end"`
	GenreFilter  string  `json:"genre_filter"`
	MovieID      string  `json:"movie_id,omitempty"`
	Score        float64 `json:"score,omitempty"`
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
	NodesInvolved   []string              `json:"nodes_involved,omitempty"`
	Recommendations []MovieRecommendation `json:"recommendations"`
}

type ContainerInfo struct {
	ID         string `json:"id"`
	Names      string `json:"name"`
	State      string `json:"state"`
	Status     string `json:"status"`
	CPUUsage   string `json:"cpu_usage"`
	MemUsage   string `json:"mem_usage"`
	MemLimit   string `json:"mem_limit"`
	MemPercent string `json:"mem_percent"`
}

type DockerStats struct {
	CPUStats    CPUStats    `json:"cpu_stats"`
	PreCPUStats CPUStats    `json:"precpu_stats"`
	MemoryStats MemoryStats `json:"memory_stats"`
}

type CPUStats struct {
	CPUUsage       CPUUsage `json:"cpu_usage"`
	SystemCPUUsage uint64   `json:"system_cpu_usage"`
	OnlineCPUs     int      `json:"online_cpus"`
}

type CPUUsage struct {
	TotalUsage uint64 `json:"total_usage"`
}

type MemoryStats struct {
	Usage uint64 `json:"usage"`
	Limit uint64 `json:"limit"`
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
		log.Fatalf("Error conectando a Mongo: %v", err)
	}

	http.HandleFunc("/login", handleLogin)
	http.HandleFunc("/register", corsMiddleware(handleRegister))

	http.HandleFunc("/recommend", corsMiddleware(authMiddleware(handleRecommend)))
	http.HandleFunc("/movies", corsMiddleware(authMiddleware(handleGetMovies)))
	http.HandleFunc("/history", corsMiddleware(authMiddleware(handleGetHistory)))
	http.HandleFunc("/rate", corsMiddleware(authMiddleware(handleAddRating)))
	http.HandleFunc("/stats", corsMiddleware(authMiddleware(handleStats)))
	http.HandleFunc("/admin/containers", corsMiddleware(authMiddleware(handleDockerContainers)))
	http.HandleFunc("/admin/logs", corsMiddleware(authMiddleware(handleDockerLogs)))

	// Ahora (Sin Auth):
	// http.HandleFunc("/recommend", corsMiddleware(handleRecommend))
	// http.HandleFunc("/admin/containers", corsMiddleware(handleDockerContainers))
	// http.HandleFunc("/admin/logs", corsMiddleware(handleDockerLogs))

	log.Printf("API Coordinador lista en %s", PORT)
	http.ListenAndServe(PORT, nil)
}

func corsMiddleware(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Access-Control-Allow-Origin", "*")
		w.Header().Set("Access-Control-Allow-Methods", "GET, POST, OPTIONS")
		w.Header().Set("Access-Control-Allow-Headers", "Content-Type, Authorization")

		if r.Method == "OPTIONS" {
			w.WriteHeader(http.StatusOK)
			return
		}
		next(w, r)
	}
}

func authMiddleware(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {

		if r.Method == "OPTIONS" {
			next(w, r)
			return
		}

		authHeader := r.Header.Get("Authorization")
		if authHeader == "" {
			http.Error(w, `{"error": "Se requiere autenticación (Header Authorization faltante)"}`, http.StatusUnauthorized)
			return
		}

		parts := strings.Split(authHeader, " ")
		if len(parts) != 2 || parts[0] != "Bearer" {
			http.Error(w, `{"error": "Formato de token inválido. Use 'Bearer <token>'"}`, http.StatusUnauthorized)
			return
		}
		tokenString := parts[1]

		claims := &UserClaims{}
		token, err := jwt.ParseWithClaims(tokenString, claims, func(token *jwt.Token) (interface{}, error) {
			return JWT_SECRET, nil
		})

		if err != nil || !token.Valid {
			http.Error(w, `{"error": "Token inválido o expirado"}`, http.StatusUnauthorized)
			return
		}

		log.Printf("Acceso autorizado para usuario: %s", claims.UserID)
		next(w, r)
	}
}

func handleRegister(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	if r.Method == "OPTIONS" {
		return
	}

	var req RegisterRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, `{"error": "Cuerpo de solicitud inválido"}`, http.StatusBadRequest)
		return
	}

	if req.Username == "" || req.Password == "" || len(req.Password) < 3 {
		http.Error(w, `{"error": "Datos incompletos. Se requiere username y password (min 3 chars)"}`, http.StatusBadRequest)
		return
	}

	collUsers := mongoClient.Database("movielens").Collection("users")

	var existingUser bson.M
	err := collUsers.FindOne(ctx, bson.D{{Key: "username", Value: req.Username}}).Decode(&existingUser)
	if err == nil {
		http.Error(w, `{"error": "El nombre de usuario ya está en uso"}`, http.StatusConflict)
		return
	}

	newUserID := strconv.FormatInt(time.Now().UnixNano()/1000000, 10)

	if len(newUserID) > 10 {
		newUserID = newUserID[len(newUserID)-10:]
	}

	newUserDoc := bson.D{
		{Key: "user_id", Value: newUserID},
		{Key: "username", Value: req.Username},
		{Key: "password", Value: req.Password},
		{Key: "created_at", Value: time.Now()},
	}
	_, err = collUsers.InsertOne(ctx, newUserDoc)
	if err != nil {
		log.Printf("Error insertando nuevo usuario: %v", err)
		http.Error(w, `{"error": "Error interno al crear usuario"}`, http.StatusInternalServerError)
		return
	}

	w.WriteHeader(http.StatusCreated)
	json.NewEncoder(w).Encode(map[string]string{
		"message":  "Registro exitoso",
		"user_id":  newUserID,
		"username": req.Username,
	})
}

func handleLogin(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Access-Control-Allow-Origin", "*")
	w.Header().Set("Content-Type", "application/json")
	if r.Method == "OPTIONS" {
		return
	}

	var req LoginRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, `{"error": "Invalid request body"}`, http.StatusBadRequest)
		return
	}

	userIDFound := ""
	userNameFound := ""

	collUsers := mongoClient.Database("movielens").Collection("users")
	var dbUser bson.M

	err := collUsers.FindOne(ctx, bson.D{{Key: "username", Value: req.UserID}}).Decode(&dbUser)

	if err == nil {

		dbPass, _ := dbUser["password"].(string)
		if dbPass != req.Password {
			http.Error(w, `{"error": "Contraseña incorrecta"}`, http.StatusUnauthorized)
			return
		}

		userIDFound = dbUser["user_id"].(string)
		userNameFound = req.UserID
	} else {

		if _, err := strconv.Atoi(req.UserID); err == nil {
			if req.Password == req.UserID {

				userIDFound = req.UserID
				userNameFound = generateFakeName(req.UserID)
			} else {
				http.Error(w, `{"error": "Contraseña incorrecta (Para dataset: Pass=ID)"}`, http.StatusUnauthorized)
				return
			}
		} else {

			http.Error(w, `{"error": "Usuario no registrado"}`, http.StatusUnauthorized)
			return
		}
	}

	claims := UserClaims{
		UserID: userIDFound,
		RegisteredClaims: jwt.RegisteredClaims{
			ExpiresAt: jwt.NewNumericDate(time.Now().Add(24 * time.Hour)),
			Issuer:    "movielens-api",
		},
	}
	token := jwt.NewWithClaims(jwt.SigningMethodHS256, claims)
	tokenString, _ := token.SignedString(JWT_SECRET)

	json.NewEncoder(w).Encode(LoginResponse{
		Token:    tokenString,
		UserName: userNameFound,
	})
}

func handleAddRating(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	if r.Method == "OPTIONS" {
		return
	}

	var req RateRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, `{"error": "Cuerpo de solicitud inválido"}`, http.StatusBadRequest)
		return
	}

	// 1. Validación básica
	if req.UserID == "" || req.MovieID == "" || req.Score < 0.5 || req.Score > 5.0 {
		http.Error(w, `{"error": "Datos incompletos o calificación fuera de rango (0.5 a 5.0)"}`, http.StatusBadRequest)
		return
	}

	// 2. Guardar/Actualizar en MongoDB (Persistencia)
	collRatings := mongoClient.Database("movielens").Collection("ratings")

	// Upsert: Si existe actualiza, si no crea
	filter := bson.D{{Key: "user_id", Value: req.UserID}, {Key: "movie_id", Value: req.MovieID}}
	update := bson.D{{Key: "$set", Value: bson.D{{Key: "score", Value: req.Score}}}}
	opts := options.Update().SetUpsert(true)

	result, err := collRatings.UpdateOne(ctx, filter, update, opts)
	if err != nil {
		log.Printf("Error Upsert en Mongo: %v", err)
		http.Error(w, `{"error": "Error interno al guardar la calificación"}`, http.StatusInternalServerError)
		return
	}
	go broadcastUpdateToCluster(req.UserID, req.MovieID, req.Score)

	cacheKeyPrefix := "rec:" + req.UserID + ":"
	keys, err := redisClient.Keys(ctx, cacheKeyPrefix+"*").Result()
	if err == nil && len(keys) > 0 {
		redisClient.Del(ctx, keys...)
		log.Printf("🗑️ Cache limpia para Usuario %s (%d claves)", req.UserID, len(keys))
	}

	message := "Calificación guardada exitosamente."
	if result.UpsertedID != nil {
		message = "Nueva calificación insertada."
	} else if result.ModifiedCount > 0 {
		message = "Calificación actualizada."
	}

	w.WriteHeader(http.StatusOK)
	json.NewEncoder(w).Encode(map[string]string{
		"message": message,
	})
}

func broadcastUpdateToCluster(userID, movieID string, score float64) {
	log.Printf("Broadcasting update para User %s a %d nodos...", userID, len(WORKER_ADDRS))

	for _, addr := range WORKER_ADDRS {

		go func(address string) {
			conn, err := net.DialTimeout("tcp", address, 2*time.Second)
			if err != nil {
				log.Printf("Error update nodo %s: %v", address, err)
				return
			}
			defer conn.Close()

			req := WorkerRequest{
				OpType:       "UPDATE",
				TargetUserID: userID,
				MovieID:      movieID,
				Score:        score,
			}
			if err := json.NewEncoder(conn).Encode(req); err != nil {
				log.Printf("Error enviando JSON a %s: %v", address, err)
				return
			}

		}(addr)
	}
}

func handleGetHistory(w http.ResponseWriter, r *http.Request) {
	userID := r.URL.Query().Get("user_id")

	collRatings := mongoClient.Database("movielens").Collection("ratings")
	filter := bson.D{{Key: "user_id", Value: userID}}

	opts := options.Find().SetLimit(20).SetSort(bson.D{{Key: "score", Value: -1}})

	cursor, _ := collRatings.Find(ctx, filter, opts)
	var history []RatingHistory
	cursor.All(ctx, &history)

	collMovies := mongoClient.Database("movielens").Collection("movies")
	for i, item := range history {
		var movie Movie
		collMovies.FindOne(ctx, bson.D{{Key: "_id", Value: item.MovieID}}).Decode(&movie)
		history[i].Title = movie.Title
	}

	json.NewEncoder(w).Encode(history)
}

func handleStats(w http.ResponseWriter, r *http.Request) {
	var m runtime.MemStats
	runtime.ReadMemStats(&m)

	stats := ClusterStats{
		ActiveWorkers: len(WORKER_ADDRS),
		CPUNum:        runtime.NumCPU(),
		Goroutines:    runtime.NumGoroutine(),
		MemoryUsage:   m.Alloc / 1024 / 1024,
	}
	json.NewEncoder(w).Encode(stats)
}

func handleGetMovies(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Access-Control-Allow-Origin", "*")
	w.Header().Set("Content-Type", "application/json")
	query := r.URL.Query().Get("q")
	limit, _ := strconv.ParseInt(r.URL.Query().Get("limit"), 10, 64)
	skip, _ := strconv.ParseInt(r.URL.Query().Get("skip"), 10, 64)
	if limit == 0 {
		limit = 20
	}

	filter := bson.D{}
	if query != "" {

		filter = bson.D{{Key: "title", Value: bson.D{{Key: "$regex", Value: query}, {Key: "$options", Value: "i"}}}}
	}

	coll := mongoClient.Database("movielens").Collection("movies")
	count, _ := coll.CountDocuments(ctx, filter)

	opts := options.Find().SetLimit(limit).SetSkip(skip)
	cursor, _ := coll.Find(ctx, filter, opts)

	var movies []Movie
	cursor.All(ctx, &movies)

	json.NewEncoder(w).Encode(map[string]interface{}{
		"total":  count,
		"page":   skip/limit + 1,
		"movies": movies,
	})
}

func handleRecommend(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	start := time.Now()
	userID := r.URL.Query().Get("user_id")
	genre := r.URL.Query().Get("genre")
	fakeName := generateFakeName(userID)
	cacheKey := "rec:" + userID + ":" + genre

	val, err := redisClient.Get(ctx, cacheKey).Result()
	if err == nil {

		var cachedRecs []MovieRecommendation
		json.Unmarshal([]byte(val), &cachedRecs)

		respObj := APIResponse{
			Source:          "Cache (Redis)",
			ProcessingTime:  time.Since(start).String(),
			FilterUsed:      genre,
			UserName:        fakeName,
			Recommendations: cachedRecs,
		}
		log.Printf("[CACHE HIT] Respuesta rápida para Usuario %s", userID)
		json.NewEncoder(w).Encode(respObj)
		return
	}

	log.Printf("[CACHE MISS] Usuario %s. Iniciando Scatter-Gather a %d nodos...", userID, len(WORKER_ADDRS))

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
			log.Printf(" -> Enviando tarea a %s (Rango %d-%d)", address, start, end)

			resp, err := callWorker(address, userID, start, end, genre)
			if err == nil {
				log.Printf(" <- Recibido de %s (NodoID: %s)", address, resp.NodeID)
				resultsChan <- *resp
			} else {
				log.Printf("Error en nodo %s: %v", address, err)
			}
		}(addr, rStart, rEnd)
	}

	wg.Wait()
	close(resultsChan)

	movieScores := make(map[string]float64)
	movieTitles := make(map[string]string)
	totalProc := 0
	var nodesInvolved []string

	for resp := range resultsChan {
		totalProc += resp.ProcessedCount
		nodesInvolved = append(nodesInvolved, resp.NodeID)

		for _, rec := range resp.Recommendations {
			movieScores[rec.MovieID] += rec.Score
			if rec.Title != "" {
				movieTitles[rec.MovieID] = rec.Title
			}
		}
	}

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

	respObj := APIResponse{
		Source:          "Distributed Cluster (Scatter-Gather)",
		ProcessingTime:  time.Since(start).String(),
		TotalProcessed:  totalProc,
		FilterUsed:      genre,
		UserName:        fakeName,
		NodesInvolved:   nodesInvolved,
		Recommendations: finalRecs,
	}

	recsBytes, _ := json.Marshal(finalRecs)
	redisClient.Set(ctx, cacheKey, recsBytes, 10*time.Minute)

	log.Printf("[FIN] Procesado en %v. Nodos: %v", time.Since(start), nodesInvolved)
	json.NewEncoder(w).Encode(respObj)
}

func callWorker(addr, userID string, start, end int, genre string) (*WorkerResponse, error) {
	conn, err := net.DialTimeout("tcp", addr, 2*time.Second)
	if err != nil {
		return nil, err
	}
	defer conn.Close()

	req := WorkerRequest{
		OpType:       "RECOMMEND",
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

func handleDockerContainers(w http.ResponseWriter, r *http.Request) {
	// Conexión automática usando variables de entorno o socket por defecto
	cli, err := client.NewClientWithOpts(client.FromEnv, client.WithAPIVersionNegotiation())
	if err != nil {
		http.Error(w, "Error conectando a Docker: "+err.Error(), 500)
		return
	}
	defer cli.Close()

	containers, err := cli.ContainerList(ctx, types.ContainerListOptions{})
	if err != nil {
		http.Error(w, "Error listando: "+err.Error(), 500)
		return
	}

	var result []ContainerInfo

	for _, c := range containers {
		// Stats snapshot (stream=false)
		statsBody, err := cli.ContainerStats(ctx, c.ID, false)

		cpuPercent := 0.0
		memUsage := 0.0
		memLimit := 0.0
		memPercent := 0.0

		if err == nil {
			var stats DockerStats
			json.NewDecoder(statsBody.Body).Decode(&stats)
			statsBody.Body.Close()

			// Cálculo CPU
			cpuDelta := float64(stats.CPUStats.CPUUsage.TotalUsage) - float64(stats.PreCPUStats.CPUUsage.TotalUsage)
			systemDelta := float64(stats.CPUStats.SystemCPUUsage) - float64(stats.PreCPUStats.SystemCPUUsage)
			numCPUs := float64(stats.CPUStats.OnlineCPUs)
			if numCPUs == 0.0 {
				numCPUs = float64(runtime.NumCPU())
			}

			if systemDelta > 0.0 && cpuDelta > 0.0 {
				cpuPercent = (cpuDelta / systemDelta) * numCPUs * 100.0
			}

			// Cálculo RAM
			memUsage = float64(stats.MemoryStats.Usage) / 1024 / 1024
			memLimit = float64(stats.MemoryStats.Limit) / 1024 / 1024
			if memLimit > 0 {
				memPercent = (memUsage / memLimit) * 100.0
			}
		}

		name := "unknown"
		if len(c.Names) > 0 {
			name = c.Names[0]
		}

		info := ContainerInfo{
			ID:         c.ID[:10],
			Names:      name,
			State:      c.State,
			Status:     c.Status,
			CPUUsage:   fmt.Sprintf("%.2f%%", cpuPercent),
			MemUsage:   fmt.Sprintf("%.0f MB", memUsage),
			MemLimit:   fmt.Sprintf("%.0f MB", memLimit),
			MemPercent: fmt.Sprintf("%.2f%%", memPercent),
		}
		result = append(result, info)
	}

	json.NewEncoder(w).Encode(result)
}

func handleDockerLogs(w http.ResponseWriter, r *http.Request) {
	containerID := r.URL.Query().Get("id")
	if containerID == "" {
		http.Error(w, "Falta parámetro 'id'", 400)
		return
	}

	cli, err := client.NewClientWithOpts(client.FromEnv, client.WithAPIVersionNegotiation())
	if err != nil {
		http.Error(w, "Error Docker: "+err.Error(), 500)
		return
	}
	defer cli.Close()

	opts := types.ContainerLogsOptions{ShowStdout: true, ShowStderr: true, Tail: "50"}
	out, err := cli.ContainerLogs(ctx, containerID, opts)
	if err != nil {
		http.Error(w, "Error logs: "+err.Error(), 500)
		return
	}
	defer out.Close()

	logs, _ := io.ReadAll(out)
	w.Header().Set("Content-Type", "text/plain")
	w.Write(logs)
}
