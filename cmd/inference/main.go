// Inference Service entrypoint.
// ConnectRPC server that proxies classification requests to Triton GPU.
//
// Architecture:
//   Engine → ConnectRPC → Inference Service (this binary)
//                       → HTTP → Triton (GPU, SigLIP2 ONNX)
//
// Usage:
//   INFERENCE_CONFIG_PATH=inference/config.yaml inference
//
// Signals:
//   SIGHUP — reload configuration (hot-reload prompts and thresholds)

package main

import (
	"context"
	"flag"
	"fmt"
	"log"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"connectrpc.com/connect"
	"golang.org/x/net/http2"
	"golang.org/x/net/http2/h2c"

	"github.com/soyAurelio/AurelioMod/inference"
	"github.com/soyAurelio/AurelioMod/inference/server"
	v1connect "github.com/soyAurelio/AurelioMod/proto/aureliomod/v1/aureliomodv1connect"
)

func main() {
	configPath := flag.String("config", os.Getenv("INFERENCE_CONFIG_PATH"), "Path to config YAML")
	inputIDsPath := flag.String("input-ids", os.Getenv("INFERENCE_INPUT_IDS_PATH"), "Path to input_ids JSON")
	addr := flag.String("addr", ":"+os.Getenv("PORT"), "Listen address")
	healthcheck := flag.Bool("healthcheck", false, "Run health check and exit")
	flag.Parse()

	if *healthcheck {
		os.Exit(runHealthcheck(*addr))
	}

	if *configPath == "" {
		*configPath = "inference/config.yaml"
	}
	if *inputIDsPath == "" {
		*inputIDsPath = "models/siglip2-512/input_ids.json"
	}
	if *addr == ":" {
		*addr = ":8080"
	}

	log.SetFlags(log.LstdFlags | log.Lshortfile)

	// Load configuration
	cfg, err := inference.LoadConfig(*configPath)
	if err != nil {
		log.Fatalf("load config: %v", err)
	}
	if err := cfg.LoadInputIDs(*inputIDsPath); err != nil {
		log.Fatalf("load input_ids: %v", err)
	}
	log.Printf("config loaded: %d categories, %d total prompts",
		len(cfg.Classifier.Categories), cfg.Classifier.TotalPrompts())

	// Create server
	srv, err := server.New(cfg)
	if err != nil {
		log.Fatalf("create server: %v", err)
	}

	// SIGHUP handler for hot-reload
	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGHUP)
	go func() {
		for range sigCh {
			log.Println("SIGHUP received, reloading config...")
			if err := srv.Reload(*configPath); err != nil {
				log.Printf("reload failed: %v", err)
				continue
			}
			// Reload input_ids too
			cfg, _ := inference.LoadConfig(*configPath)
			if cfg != nil {
				cfg.LoadInputIDs(*inputIDsPath)
			}
			log.Println("config reloaded successfully")
		}
	}()

	// Set up ConnectRPC handler
	mux := http.NewServeMux()
	path, handler := v1connect.NewInferenceServiceHandler(srv,
		connect.WithCompressMinBytes(1024),
	)
	mux.Handle(path, handler)

	// Health check
	mux.HandleFunc("/healthz", func(w http.ResponseWriter, r *http.Request) {
		ready, err := srv.TritonHealth(r.Context())
		if err != nil || !ready {
			w.WriteHeader(http.StatusServiceUnavailable)
			fmt.Fprintf(w, "not ready: %v", err)
			return
		}
		w.WriteHeader(http.StatusOK)
		fmt.Fprintf(w, "ok")
	})

	httpServer := &http.Server{
		Addr:    *addr,
		Handler: h2c.NewHandler(mux, &http2.Server{}),
	}

	log.Printf("inference service listening on %s", *addr)
	if err := httpServer.ListenAndServe(); err != nil && err != http.ErrServerClosed {
		log.Fatalf("server: %v", err)
	}
}

func runHealthcheck(addr string) int {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	req, err := http.NewRequestWithContext(ctx, http.MethodGet,
		"http://"+addr+"/healthz", nil)
	if err != nil {
		log.Printf("healthcheck: %v", err)
		return 1
	}

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		log.Printf("healthcheck: %v", err)
		return 1
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		log.Printf("healthcheck: status %d", resp.StatusCode)
		return 1
	}
	return 0
}

// Ensure context import is used
var _ context.Context
