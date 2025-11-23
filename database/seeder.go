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

type Movie struct {
	ID     string `bson:"_id"`
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

	clientOptions := options.Client().ApplyURI("mongodb://localhost:27017")
	client, err := mongo.Connect(context.TODO(), clientOptions)
	if err != nil {
		log.Fatal("Error conectando al cliente Mongo:", err)
	}
	defer client.Disconnect(context.TODO())

	err = client.Ping(context.TODO(), nil)
	if err != nil {
		log.Fatal("No se pudo hacer ping a MongoDB. ¿Está corriendo el contenedor?:", err)
	}
	fmt.Println("Conectado exitosamente a MongoDB!")

	db := client.Database("movielens")

	db.Collection("movies").Drop(context.TODO())
	db.Collection("ratings").Drop(context.TODO())
	fmt.Println("Base de datos limpia.")

	loadMoviesToMongo("../ml-10M100K/movies.dat", db.Collection("movies"))

	// 3. Cargar Ratings
	loadRatingsToMongo("../ml-10M100K/ratings.dat", db.Collection("ratings"))

	fmt.Printf("\n¡Proceso terminado en %v!\n", time.Since(start))
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

		if len(batch) >= 2000 {
			_, err := collection.InsertMany(context.TODO(), batch)
			if err != nil {
				log.Printf("Error insertando lote de películas: %v", err)
			}
			batch = nil
		}
	}

	if len(batch) > 0 {
		collection.InsertMany(context.TODO(), batch)
	}
	fmt.Printf("Total Películas insertadas: %d\n", count)
}

func loadRatingsToMongo(filePath string, collection *mongo.Collection) {
	fmt.Println("--- Cargando Ratings ---")
	file, err := os.Open(filePath)
	if err != nil {
		log.Fatalf("No se encontró el archivo %s: %v", filePath, err)
	}
	defer file.Close()

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

		score, _ := strconv.ParseFloat(parts[2], 64)

		rating := Rating{
			UserID:  parts[0],
			MovieID: parts[1],
			Score:   score,
		}
		batch = append(batch, rating)
		count++

		if len(batch) >= 10000 {
			_, err := collection.InsertMany(context.TODO(), batch)
			if err != nil {
				log.Printf("Error insertando lote de ratings: %v", err)
			}
			batch = nil

			if count%100000 == 0 {
				fmt.Printf("\rInsertados: %d ratings...", count)
			}
		}
	}

	if len(batch) > 0 {
		collection.InsertMany(context.TODO(), batch)
	}
	fmt.Printf("\nTotal Ratings insertados: %d\n", count)
}
