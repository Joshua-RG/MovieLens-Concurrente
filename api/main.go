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
	"runtime"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

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
	TOTAL_USERS = 70000
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
	NodesInvolved   []string              `json:"nodes_involved,omitempty"`
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
		log.Fatalf("Error conectando a Mongo: %v", err)
	}

	http.HandleFunc("/login", handleLogin)
	http.HandleFunc("/recommend", corsMiddleware(handleRecommend))
	http.HandleFunc("/movies", corsMiddleware(handleGetMovies))
	http.HandleFunc("/history", corsMiddleware(handleGetHistory))
	http.HandleFunc("/stats", corsMiddleware(handleStats))
	http.HandleFunc("/register", corsMiddleware(handleRegister))
	http.HandleFunc("/rate", corsMiddleware(handleAddRating))

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

	if _, err := strconv.Atoi(req.UserID); err != nil {
		w.WriteHeader(http.StatusUnauthorized)
		json.NewEncoder(w).Encode(map[string]string{"error": "El Usuario debe ser un ID numérico"})
		return
	}

	if req.Password != req.UserID {
		w.WriteHeader(http.StatusUnauthorized)
		json.NewEncoder(w).Encode(map[string]string{"error": "Contraseña incorrecta (Tip: La contraseña es igual al ID)"})
		return
	}

	claims := UserClaims{
		UserID: req.UserID,
		RegisteredClaims: jwt.RegisteredClaims{
			ExpiresAt: jwt.NewNumericDate(time.Now().Add(24 * time.Hour)),
			Issuer:    "movielens-api",
		},
	}
	token := jwt.NewWithClaims(jwt.SigningMethodHS256, claims)
	tokenString, _ := token.SignedString(JWT_SECRET)

	json.NewEncoder(w).Encode(LoginResponse{
		Token:    tokenString,
		UserName: generateFakeName(req.UserID),
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

	if req.UserID == "" || req.MovieID == "" || req.Score < 0.5 || req.Score > 5.0 {
		http.Error(w, `{"error": "Datos incompletos o calificación fuera de rango (0.5 a 5.0)"}`, http.StatusBadRequest)
		return
	}

	collRatings := mongoClient.Database("movielens").Collection("ratings")

	filter := bson.D{{Key: "user_id", Value: req.UserID}, {Key: "movie_id", Value: req.MovieID}}
	update := bson.D{{Key: "$set", Value: bson.D{{Key: "score", Value: req.Score}}}}
	opts := options.Update().SetUpsert(true)

	result, err := collRatings.UpdateOne(ctx, filter, update, opts)
	if err != nil {
		log.Printf("Error Upsert en ratings: %v", err)
		http.Error(w, `{"error": "Error interno al guardar la calificación"}`, http.StatusInternalServerError)
		return
	}

	cacheKeyPrefix := "rec:" + req.UserID + ":"
	keys, err := redisClient.Keys(ctx, cacheKeyPrefix+"*").Result()
	if err == nil && len(keys) > 0 {
		redisClient.Del(ctx, keys...)
		log.Printf("Cache limpia para Usuario %s (%d claves borradas)", req.UserID, len(keys))
	}

	w.WriteHeader(http.StatusOK)
	message := "Calificación guardada exitosamente."
	if result.UpsertedID != nil {
		message = "Nueva calificación insertada exitosamente."
	} else if result.ModifiedCount > 0 {
		message = "Calificación existente actualizada exitosamente."
	}

	json.NewEncoder(w).Encode(map[string]string{
		"message": message,
	})
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
