package main

import (
	"encoding/csv"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"sync"
	"time"
)

const (
	BaseURL          = "http://localhost:8080"
	OutputFile       = "metricas_finales.csv"
	SamplesToCollect = 10
	MonitorInterval  = 50 * time.Millisecond
)

type APIResponse struct {
	Source          string        `json:"source"`
	ProcessingTime  string        `json:"processing_time"`
	TotalProcessed  int           `json:"total_processed_users"`
	Recommendations []interface{} `json:"recommendations"`
	Error           string        `json:"error"`
}

type ContainerInfo struct {
	Names    string `json:"name"`
	CPUUsage string `json:"cpu_usage"`
	MemUsage string `json:"mem_usage"`
}

var (
	peakCPU      float64
	peakRAM      float64
	monitorMutex sync.Mutex
	stopMonitor  chan bool
	wgMonitor    sync.WaitGroup
)

func main() {
	escenario := flag.String("escenario", "Test", "Nombre del escenario actual")
	flag.Parse()

	fmt.Printf("\nIniciando Benchmark (RAM FIX) para: [%s]\n", *escenario)

	cleanCacheTotal()

	stopMonitor = make(chan bool)
	wgMonitor.Add(1)
	go startCLIMonitoring()

	fmt.Println("Ejecutando Ronda Fría...")

	var totalColdLatency float64
	var totalColdInternal float64
	var validUsers []string
	currentUserID := 1
	samplesCount := 0

	for samplesCount < SamplesToCollect {
		uidStr := strconv.Itoa(currentUserID)
		startReq := time.Now()
		resp, err := makeRequest(uidStr)
		lat := time.Since(startReq).Seconds()

		isValid := err == nil && resp.Error == "" && resp.TotalProcessed > 0

		if isValid {
			dur, _ := parseDuration(resp.ProcessingTime)
			totalColdLatency += lat
			totalColdInternal += dur
			validUsers = append(validUsers, uidStr)
			samplesCount++
			fmt.Printf("[User %s] OK (%.4fs)\n", uidStr, lat)
		}
		currentUserID++
		if currentUserID > 5000 && samplesCount == 0 {
			log.Fatal("Error: No se encuentran usuarios válidos.")
		}
	}

	time.Sleep(200 * time.Millisecond)
	stopMonitor <- true
	wgMonitor.Wait()

	avgColdLatency := totalColdLatency / float64(samplesCount)
	avgColdInternal := totalColdInternal / float64(samplesCount)

	fmt.Println("Ejecutando Ronda Caliente...")
	var totalWarmLatency float64
	for _, uid := range validUsers {
		startReq := time.Now()
		makeRequest(uid)
		totalWarmLatency += time.Since(startReq).Seconds()
	}
	avgWarmLatency := totalWarmLatency / float64(len(validUsers))

	avgOverhead := avgColdLatency - avgColdInternal
	if avgOverhead < 0 {
		avgOverhead = 0
	}

	finalCPU := peakCPU
	finalRAM := peakRAM
	bootTime := "N/A"

	saveToCSV(*escenario, samplesCount, avgColdLatency, avgColdInternal, avgOverhead, avgWarmLatency, finalCPU, finalRAM, bootTime)

	fmt.Printf("\nRESULTADOS:\n")
	fmt.Printf("   -> CPU Pico: %.2f%%\n", finalCPU)
	fmt.Printf("   -> RAM Pico: %.0f MB\n", finalRAM)
}

func startCLIMonitoring() {
	defer wgMonitor.Done()

	args := []string{"stats", "--no-stream", "--format", "{{.Name}};{{.CPUPerc}};{{.MemUsage}}"}

	for {
		select {
		case <-stopMonitor:
			return
		default:
			cmd := exec.Command("docker", args...)
			out, err := cmd.Output()

			if err == nil {
				output := string(out)
				lines := strings.Split(output, "\n")

				var currentTotalCPU float64 = 0.0
				var currentTotalRAM float64 = 0.0

				for _, line := range lines {
					if line == "" {
						continue
					}
					parts := strings.Split(line, ";")
					if len(parts) < 3 {
						continue
					}

					name := parts[0]
					cpuStr := parts[1]
					memStr := parts[2]

					if strings.Contains(strings.ToLower(name), "worker") {
						val := parseCPU(cpuStr)
						currentTotalCPU += val

						memUsed := strings.Split(memStr, "/")[0]
						currentTotalRAM += parseRAM(strings.TrimSpace(memUsed))
					}
				}

				monitorMutex.Lock()
				if currentTotalCPU > peakCPU {
					peakCPU = currentTotalCPU
				}
				if currentTotalRAM > peakRAM {
					peakRAM = currentTotalRAM
				}
				monitorMutex.Unlock()
			}

			time.Sleep(MonitorInterval)
		}
	}
}

func parseRAM(memStr string) float64 {

	s := strings.ReplaceAll(memStr, " ", "")

	var numPart, unitPart string
	for i, r := range s {
		if (r < '0' || r > '9') && r != '.' {
			numPart = s[:i]
			unitPart = s[i:]
			break
		}
	}

	if numPart == "" {
		return 0
	}

	val, err := strconv.ParseFloat(numPart, 64)
	if err != nil {
		return 0
	}

	switch strings.ToUpper(unitPart) {
	case "GB", "GIB":
		return val * 1024
	case "MB", "MIB":
		return val
	case "KB", "KIB":
		return val / 1024
	case "B":
		return val / 1024 / 1024
	default:
		return val
	}
}

func parseCPU(cpuStr string) float64 {
	s := strings.ReplaceAll(cpuStr, "%", "")
	s = strings.TrimSpace(s)
	val, _ := strconv.ParseFloat(s, 64)
	return val
}

func cleanCacheTotal() {
	exec.Command("docker", "exec", "movielens_redis", "redis-cli", "FLUSHALL").Run()
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
	if resp.StatusCode != 200 {
		return &apiResp, fmt.Errorf("HTTP %d", resp.StatusCode)
	}
	return &apiResp, nil
}

func saveToCSV(escenario string, reqCount int, coldLat, coldInt, overhead, warmLat, cpu, ram float64, bootTime string) {
	fileExists := false
	if _, err := os.Stat(OutputFile); err == nil {
		fileExists = true
	}
	f, _ := os.OpenFile(OutputFile, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0644)
	defer f.Close()
	w := csv.NewWriter(f)
	defer w.Flush()
	if !fileExists {
		w.Write([]string{"Escenario", "Peticiones", "Latencia_Fria_s", "Procesamiento_Interno_s", "Overhead_s", "Latencia_Cache_s", "CPU_Pico_%", "RAM_Pico_MB", "Tiempo_Carga"})
	}
	w.Write([]string{
		escenario, strconv.Itoa(reqCount),
		fmt.Sprintf("%.4f", coldLat), fmt.Sprintf("%.4f", coldInt), fmt.Sprintf("%.4f", overhead),
		fmt.Sprintf("%.4f", warmLat),
		fmt.Sprintf("%.2f", cpu), fmt.Sprintf("%.0f", ram),
		bootTime,
	})
	fmt.Printf("\nGuardado en '%s'\n", OutputFile)
}

func parseDuration(dStr string) (float64, error) {
	d, err := time.ParseDuration(dStr)
	if err != nil {
		return 0, err
	}
	return d.Seconds(), nil
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
