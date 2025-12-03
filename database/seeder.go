package main

import (
	"context"
	"encoding/csv"
	"fmt"
	"io"
	"log"
	"os"
	"strconv"
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
	fmt.Println("Iniciando Seeder para MovieLens 25M (CSV)...")
	start := time.Now()

	clientOptions := options.Client().ApplyURI("mongodb://localhost:27017")
	client, err := mongo.Connect(context.TODO(), clientOptions)
	if err != nil {
		log.Fatal(err)
	}
	defer client.Disconnect(context.TODO())

	db := client.Database("movielens")

	db.Collection("movies").Drop(context.TODO())
	db.Collection("ratings").Drop(context.TODO())

	loadMoviesToMongo("../ml-25m/movies.csv", db.Collection("movies"))

	loadRatingsToMongo("../ml-25m/ratings.csv", db.Collection("ratings"))

	fmt.Printf("\n¡Proceso terminado en %v!\n", time.Since(start))
}

func loadMoviesToMongo(filePath string, collection *mongo.Collection) {
	fmt.Println("--- Cargando Películas (movies.csv) ---")
	file, err := os.Open(filePath)
	if err != nil {
		log.Fatalf("Error abriendo archivo: %v", err)
	}
	defer file.Close()

	reader := csv.NewReader(file)

	if _, err := reader.Read(); err != nil {
		log.Fatal(err)
	}

	var batch []interface{}
	count := 0

	for {
		record, err := reader.Read()
		if err == io.EOF {
			break
		}
		if err != nil {
			log.Printf("Error leyendo línea: %v", err)
			continue
		}

		movie := Movie{
			ID:     record[0],
			Title:  record[1],
			Genres: record[2],
		}
		batch = append(batch, movie)
		count++

		if len(batch) >= 2000 {
			collection.InsertMany(context.TODO(), batch)
			batch = nil
		}
	}
	if len(batch) > 0 {
		collection.InsertMany(context.TODO(), batch)
	}
	fmt.Printf("Total Películas insertadas: %d\n", count)
}

func loadRatingsToMongo(filePath string, collection *mongo.Collection) {
	fmt.Println("--- Cargando Ratings (ratings.csv) ---")
	file, err := os.Open(filePath)
	if err != nil {
		log.Fatalf("Error abriendo archivo: %v", err)
	}
	defer file.Close()

	indexModel := mongo.IndexModel{Keys: bson.D{{Key: "user_id", Value: 1}}}
	collection.Indexes().CreateOne(context.TODO(), indexModel)

	reader := csv.NewReader(file)

	if _, err := reader.Read(); err != nil {
		log.Fatal(err)
	}

	var batch []interface{}
	count := 0

	for {
		record, err := reader.Read()
		if err == io.EOF {
			break
		}
		if err != nil {
			continue
		}

		score, _ := strconv.ParseFloat(record[2], 64)

		rating := Rating{
			UserID:  record[0],
			MovieID: record[1],
			Score:   score,
		}
		batch = append(batch, rating)
		count++

		if len(batch) >= 10000 {
			collection.InsertMany(context.TODO(), batch)
			batch = nil
			if count%500000 == 0 {
				fmt.Printf("\rInsertados: %d ratings...", count)
			}
		}
	}
	if len(batch) > 0 {
		collection.InsertMany(context.TODO(), batch)
	}
	fmt.Printf("\nTotal Ratings insertados: %d\n", count)
}
