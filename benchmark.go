package main

import (
	"encoding/csv"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"log"
	"math/rand"
	"net/http"
	"os"
	"strconv"
	"strings"
	"time"
)

const (
	BaseURL    = "http://localhost:8080"
	OutputFile = "metricas.csv"
)

type APIResponse struct {
	Source         string `json:"source"`
	ProcessingTime string `json:"processing_time"`
}

type ContainerInfo struct {
	Names    string `json:"name"`
	CPUUsage string `json:"cpu_usage"`
	MemUsage string `json:"mem_usage"`
}

func main() {

	escenario := flag.String("escenario", "Test", "Nombre del escenario actual")
	flag.Parse()

	fmt.Printf("\nIniciando Benchmark para: [%s]\n", *escenario)

	userID := strconv.Itoa(rand.Intn(5000) + 100)

	fmt.Print("Ejecutando Petición Principal (Cálculo)... ")
	start := time.Now()
	resp1, err := makeRequest(userID)
	if err != nil {
		log.Fatalf("Error: %v", err)
	}
	latenciaTotal := time.Since(start).Seconds()

	tiempoInterno, _ := parseDuration(resp1.ProcessingTime)

	overhead := latenciaTotal - tiempoInterno
	if overhead < 0 {
		overhead = 0
	}

	fmt.Printf("OK (%.4fs)\n", latenciaTotal)
	time.Sleep(500 * time.Millisecond)

	fmt.Print("Ejecutando Petición Caché... ")
	startCache := time.Now()
	_, err = makeRequest(userID)
	if err != nil {
		log.Printf("Error en caché: %v", err)
	}
	latenciaCache := time.Since(startCache).Seconds()
	fmt.Printf("OK (%.4fs)\n", latenciaCache)

	fmt.Print("Consultando métricas del Clúster... ")
	containers, err := getDockerStats()
	if err != nil {
		log.Fatalf("Error obteniendo stats: %v", err)
	}

	var cpuTotal float64 = 0.0
	var ramTotal float64 = 0.0
	var nodosActivos int = 0

	for _, c := range containers {

		if strings.Contains(c.Names, "worker") {
			nodosActivos++

			cpuVal := parseCPU(c.CPUUsage)
			ramVal := parseRAM(c.MemUsage)

			cpuTotal += cpuVal
			ramTotal += ramVal
		}
	}
	fmt.Printf("OK (%d Nodos)\n", nodosActivos)

	fileExists := false
	if _, err := os.Stat(OutputFile); err == nil {
		fileExists = true
	}

	f, err := os.OpenFile(OutputFile, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0644)
	if err != nil {
		log.Fatal(err)
	}
	defer f.Close()

	w := csv.NewWriter(f)
	defer w.Flush()

	if !fileExists {
		header := []string{
			"Escenario",
			"Latencia Total (s)",
			"Tiempo Interno (s)",
			"Overhead (s)",
			"Latencia Cache (s)",
			"CPU Total (%)",
			"RAM Total (MB)",
			"Nodos Activos",
		}
		w.Write(header)
	}

	row := []string{
		*escenario,
		fmt.Sprintf("%.4f", latenciaTotal),
		fmt.Sprintf("%.4f", tiempoInterno),
		fmt.Sprintf("%.4f", overhead),
		fmt.Sprintf("%.4f", latenciaCache),
		fmt.Sprintf("%.2f%%", cpuTotal),
		fmt.Sprintf("%.0f", ramTotal),
		strconv.Itoa(nodosActivos),
	}
	w.Write(row)

	fmt.Printf("\nResultados guardados en '%s'\n", OutputFile)
	fmt.Printf("   -> CPU Total: %.2f%%\n", cpuTotal)
	fmt.Printf("   -> RAM Total: %.0f MB\n", ramTotal)
}

func makeRequest(userID string) (*APIResponse, error) {
	url := fmt.Sprintf("%s/recommend?user_id=%s", BaseURL, userID)
	resp, err := http.Get(url)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	body, _ := io.ReadAll(resp.Body)
	var apiResp APIResponse
	json.Unmarshal(body, &apiResp)
	return &apiResp, nil
}

func getDockerStats() ([]ContainerInfo, error) {
	resp, err := http.Get(fmt.Sprintf("%s/admin/containers", BaseURL))
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	var containers []ContainerInfo
	json.NewDecoder(resp.Body).Decode(&containers)
	return containers, nil
}

func parseDuration(dStr string) (float64, error) {
	d, err := time.ParseDuration(dStr)
	if err != nil {
		return 0, err
	}
	return d.Seconds(), nil
}

func parseCPU(cpuStr string) float64 {
	s := strings.ReplaceAll(cpuStr, "%", "")
	val, _ := strconv.ParseFloat(s, 64)
	return val
}

func parseRAM(memStr string) float64 {
	parts := strings.Fields(memStr)
	if len(parts) < 2 {
		return 0
	}
	val, _ := strconv.ParseFloat(parts[0], 64)
	unit := parts[1]

	switch unit {
	case "GB", "GiB":
		return val * 1024
	case "kB", "KiB":
		return val / 1024
	default:
		return val
	}
}
