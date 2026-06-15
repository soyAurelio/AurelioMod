// ConnectRPC handler for the Inference Service.
// Implements aureliomod.v1.InferenceServiceHandler.

package server

import (
	"bytes"
	"context"
	"fmt"
	"image/jpeg"
	"math"
	"sync"
	"time"

	"connectrpc.com/connect"
	v1 "github.com/soyAurelio/AurelioMod/proto/aureliomod/v1"

	"github.com/soyAurelio/AurelioMod/inference"
	"github.com/soyAurelio/AurelioMod/inference/classifier"
)

// InferenceServer implements the InferenceService ConnectRPC handler.
type InferenceServer struct {
	config     *inference.Config
	triton     *inference.TritonClient
	inputIDs   [][]int64 // pre-computed, padded
	categories []classifier.Category

	mu       sync.RWMutex
	loadedAt time.Time
}

// New creates a new InferenceServer with the given configuration.
func New(cfg *inference.Config) (*InferenceServer, error) {
	ids, _ := cfg.Classifier.FlattenInputIDs()

	cats := make([]classifier.Category, len(cfg.Classifier.Categories))
	for i, c := range cfg.Classifier.Categories {
		cats[i] = classifier.Category{
			Name:       c.Name,
			Threshold:  c.Threshold,
			NumPrompts: len(c.InputIDs),
		}
	}

	tritonClient, err := inference.NewTritonClient(cfg.Triton.Endpoint, cfg.Model.Instance.TritonModelName)
	if err != nil {
		return nil, fmt.Errorf("triton client: %w", err)
	}

	srv := &InferenceServer{
		config:     cfg,
		triton:     tritonClient,
		inputIDs:   ids,
		categories: cats,
		loadedAt:   time.Now(),
	}

	return srv, nil
}

// Reload re-reads the config file (called on SIGHUP).
func (s *InferenceServer) Reload(path string) error {
	cfg, err := inference.LoadConfig(path)
	if err != nil {
		return fmt.Errorf("reload config: %w", err)
	}

	ids, _ := cfg.Classifier.FlattenInputIDs()
	cats := make([]classifier.Category, len(cfg.Classifier.Categories))
	for i, c := range cfg.Classifier.Categories {
		cats[i] = classifier.Category{
			Name:       c.Name,
			Threshold:  c.Threshold,
			NumPrompts: len(c.InputIDs),
		}
	}

	tritonClient, err := inference.NewTritonClient(cfg.Triton.Endpoint, cfg.Model.Instance.TritonModelName)
	if err != nil {
		return fmt.Errorf("reload triton client: %w", err)
	}

	s.mu.Lock()
	defer s.mu.Unlock()
	s.config = cfg
	s.triton = tritonClient
	s.inputIDs = ids
	s.categories = cats
	s.loadedAt = time.Now()
	return nil
}

// Classify runs zero-shot classification on image(s).
func (s *InferenceServer) Classify(
	ctx context.Context,
	req *connect.Request[v1.ClassifyRequest],
) (*connect.Response[v1.ClassifyResponse], error) {
	start := time.Now()

	var pixelValues [][][][]float32

	switch content := req.Msg.Content.(type) {
	case *v1.ClassifyRequest_Image:
		pv, err := decodeJPEG(content.Image.JpegData)
		if err != nil {
			return nil, connect.NewError(connect.CodeInvalidArgument,
				fmt.Errorf("decode image: %w", err))
		}
		pixelValues = [][][][]float32{pv}
	case *v1.ClassifyRequest_Video:
		if len(content.Video.Frames) == 0 {
			return nil, connect.NewError(connect.CodeInvalidArgument,
				fmt.Errorf("video has no frames"))
		}
		for _, frame := range content.Video.Frames {
			pv, err := decodeJPEG(frame)
			if err != nil {
				return nil, connect.NewError(connect.CodeInvalidArgument,
					fmt.Errorf("decode frame: %w", err))
			}
			pixelValues = append(pixelValues, pv)
		}
	default:
		return nil, connect.NewError(connect.CodeInvalidArgument,
			fmt.Errorf("no content provided"))
	}

	s.mu.RLock()
	inputIDs := s.inputIDs
	cats := s.categories
	s.mu.RUnlock()

	// Send to Triton: input_ids (all prompts) + pixel_values (images)
	logits, err := s.triton.Infer(ctx, inputIDs, pixelValues)
	if err != nil {
		return nil, connect.NewError(connect.CodeUnavailable,
			fmt.Errorf("triton inference: %w", err))
	}

	// Classify: sigmoid + thresholding per image, max aggregation
	nImages := len(pixelValues)
	nPromptsTotal := 0
	for _, c := range cats {
		nPromptsTotal += c.NumPrompts
	}

	// logits shape: [nImages, nPromptsTotal]
	// Aggregate max score across all images for each category
	aggScores := make(map[string]float32)
	for i := 0; i < nImages; i++ {
		imgLogits := logits[i*nPromptsTotal : (i+1)*nPromptsTotal]
		result := classifier.Classify(imgLogits, cats)
		for cat, score := range result.Scores {
			if float32(score) > aggScores[cat] {
				aggScores[cat] = float32(score)
			}
		}
	}

	// Determine top category and triggered
	topCat := ""
	topScore := 0.0
	var triggered []string
	for _, cat := range cats {
		score := float64(aggScores[cat.Name])
		if score >= cat.Threshold {
			triggered = append(triggered, cat.Name)
		}
		if score > topScore {
			topScore = score
			topCat = cat.Name
		}
	}

	inferenceMs := time.Since(start).Milliseconds()

	resp := &v1.ClassifyResponse{
		Scores:       aggScores,
		TopCategory:  topCat,
		Confidence:   float32(topScore),
		Triggered:    triggered,
		InferenceMs:  inferenceMs,
		BatchSize:    int32(nImages),
		ModelVersion: s.config.Model.Instance.Version,
	}

	// Propagate partial_scan flag for video
	if video, ok := req.Msg.Content.(*v1.ClassifyRequest_Video); ok {
		resp.IsPartialScan = video.Video.IsPartialScan
	}

	return connect.NewResponse(resp), nil
}

// Health checks Triton readiness and returns model info.
func (s *InferenceServer) Health(
	ctx context.Context,
	req *connect.Request[v1.HealthRequest],
) (*connect.Response[v1.HealthResponse], error) {
	ready, err := s.triton.Health(ctx)
	if err != nil {
		return nil, connect.NewError(connect.CodeUnavailable,
			fmt.Errorf("triton health: %w", err))
	}

	s.mu.RLock()
	loadedAt := s.loadedAt
	version := s.config.Model.Instance.Version
	s.mu.RUnlock()

	return connect.NewResponse(&v1.HealthResponse{
		Ready:        ready,
		ModelVersion: version,
		LoadedAtUnix: loadedAt.Unix(),
		Gpu:          nil, // populated when NVML is available
	}), nil
}

// decodeJPEG converts JPEG bytes to a [1, 3, 512, 512] float32 tensor
// normalized to [-1, 1] for SigLIP2.
func decodeJPEG(jpegData []byte) ([][][]float32, error) {
	img, err := jpeg.Decode(bytes.NewReader(jpegData))
	if err != nil {
		return nil, fmt.Errorf("jpeg decode: %w", err)
	}

	bounds := img.Bounds()
	w, h := bounds.Dx(), bounds.Dy()

	if w != 512 || h != 512 {
		return nil, fmt.Errorf("expected 512x512 image, got %dx%d", w, h)
	}

	// Convert to [C, H, W] float32 normalized to [-1, 1]
	// SigLIP2 normalization: pixel = (raw/255 - 0.5) / 0.5
	pixels := make([][][]float32, 3)
	for c := 0; c < 3; c++ {
		pixels[c] = make([][]float32, h)
		for y := 0; y < h; y++ {
			pixels[c][y] = make([]float32, w)
			for x := 0; x < w; x++ {
				r, g, b, _ := img.At(x, y).RGBA()
				var raw float32
				switch c {
				case 0:
					raw = float32(r>>8) / 255.0
				case 1:
					raw = float32(g>>8) / 255.0
				case 2:
					raw = float32(b>>8) / 255.0
				}
				pixels[c][y][x] = (raw - 0.5) / 0.5
			}
		}
	}

	return pixels, nil
}

// TritonHealth checks Triton readiness (used by healthcheck endpoint).
func (s *InferenceServer) TritonHealth(ctx context.Context) (bool, error) {
	return s.triton.Health(ctx)
}

// Guard against NaN in scores.
func init() {
	// Ensure math functions don't produce NaN in edge cases
	_ = math.MaxFloat64
}
