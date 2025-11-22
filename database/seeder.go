package main

import (
	"bufio"
	"context"
	"fmt"
	"log"
	"os"
	"strconv"
	"strings"
	"time"

	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/mongo"
	"go.mongodb.org/mongo-driver/mongo/options"
)

// Estructuras que mapean los datos de MovieLens a BSON para MongoDB
type Movie struct {
	ID     string `bson:"_id"` // Usamos el ID original como _id
	Title  string `bson:"title"`
	Genres string `bson:"genres"`
}

type Rating struct {
	UserID  string  `bson:"user_id"`
	MovieID string  `bson:"movie_id"`
	Score   float64 `bson:"score"`
}

func main() {
	fmt.Println("🌱 Iniciando el Seeder...")
	start := time.Now()

	// 1. Conectar a MongoDB
	// Nota: "localhost" funciona porque ejecutamos este script desde la máquina host (tu VM),
	// no desde dentro de un contenedor todavía.
	clientOptions := options.Client().ApplyURI("mongodb://localhost:27017")
	client, err := mongo.Connect(context.TODO(), clientOptions)
	if err != nil {
		log.Fatal("Error conectando al cliente Mongo:", err)
	}
	defer client.Disconnect(context.TODO())

	// Verificar conexión (Ping)
	err = client.Ping(context.TODO(), nil)
	if err != nil {
		log.Fatal("No se pudo hacer ping a MongoDB. ¿Está corriendo el contenedor?:", err)
	}
	fmt.Println("✅ Conectado exitosamente a MongoDB!")

	db := client.Database("movielens")

	// Limpiar colecciones antiguas si existen (para evitar duplicados si lo corres 2 veces)
	db.Collection("movies").Drop(context.TODO())
	db.Collection("ratings").Drop(context.TODO())
	fmt.Println("🧹 Base de datos limpia.")

	// 2. Cargar Películas
	loadMoviesToMongo("../ml-10M100K/movies.dat", db.Collection("movies"))

	// 3. Cargar Ratings
	loadRatingsToMongo("../ml-10M100K/ratings.dat", db.Collection("ratings"))

	fmt.Printf("\n🚀 ¡Proceso terminado en %v!\n", time.Since(start))
}

func loadMoviesToMongo(filePath string, collection *mongo.Collection) {
	fmt.Println("--- Cargando Películas ---")
	file, err := os.Open(filePath)
	if err != nil {
		log.Fatalf("No se encontró el archivo %s: %v", filePath, err)
	}
	defer file.Close()

	var batch []interface{}
	scanner := bufio.NewScanner(file)
	count := 0

	for scanner.Scan() {
		line := scanner.Text()
		parts := strings.Split(line, "::")
		if len(parts) < 3 {
			continue
		}

		movie := Movie{
			ID:     parts[0],
			Title:  parts[1],
			Genres: parts[2],
		}
		batch = append(batch, movie)
		count++

		// Insertar en lotes de 2000 para eficiencia
		if len(batch) >= 2000 {
			_, err := collection.InsertMany(context.TODO(), batch)
			if err != nil {
				log.Printf("Error insertando lote de películas: %v", err)
			}
			batch = nil // Vaciar el batch
		}
	}
	// Insertar los restantes
	if len(batch) > 0 {
		collection.InsertMany(context.TODO(), batch)
	}
	fmt.Printf("✅ Total Películas insertadas: %d\n", count)
}

func loadRatingsToMongo(filePath string, collection *mongo.Collection) {
	fmt.Println("--- Cargando Ratings (Esto puede tardar unos minutos) ---")
	file, err := os.Open(filePath)
	if err != nil {
		log.Fatalf("No se encontró el archivo %s: %v", filePath, err)
	}
	defer file.Close()

	// CREAR ÍNDICES (CRÍTICO PARA EL RENDIMIENTO)
	// Creamos un índice en "user_id" porque buscaremos "todos los ratings del usuario X"
	fmt.Println("Creating index on user_id...")
	indexModel := mongo.IndexModel{
		Keys: bson.D{{Key: "user_id", Value: 1}},
	}
	_, err = collection.Indexes().CreateOne(context.TODO(), indexModel)
	if err != nil {
		log.Printf("Advertencia creando índice: %v", err)
	}

	var batch []interface{}
	scanner := bufio.NewScanner(file)
	count := 0

	for scanner.Scan() {
		line := scanner.Text()
		parts := strings.Split(line, "::")
		if len(parts) < 3 {
			continue
		}

		// Convertir el rating a float
		score, _ := strconv.ParseFloat(parts[2], 64)

		rating := Rating{
			UserID:  parts[0],
			MovieID: parts[1],
			Score:   score,
		}
		batch = append(batch, rating)
		count++

		// Lotes más grandes para ratings (10,000)
		if len(batch) >= 10000 {
			_, err := collection.InsertMany(context.TODO(), batch)
			if err != nil {
				log.Printf("Error insertando lote de ratings: %v", err)
			}
			batch = nil
			// Feedback visual tipo barra de carga
			if count%100000 == 0 {
				fmt.Printf("\rInsertados: %d ratings...", count)
			}
		}
	}
	// Insertar restantes
	if len(batch) > 0 {
		collection.InsertMany(context.TODO(), batch)
	}
	fmt.Printf("\n✅ Total Ratings insertados: %d\n", count)
}
