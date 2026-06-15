// ConnectRPC handler for the Inference Service.
// Two-phase inference:
//   1. Startup: cache text embeddings (input_ids → Triton → text_embeds)
//   2. Hot path: image → Triton → image_embeds, compute cosine sim in Go

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

type InferenceServer struct {
	config     *inference.Config
	triton     *inference.TritonClient
	inputIDs   [][]int64
	categories []classifier.Category

	mu         sync.RWMutex
	textEmbeds [][]float32
	loadedAt   time.Time
}

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

	if err := srv.computeTextEmbeds(context.Background()); err != nil {
		return nil, fmt.Errorf("compute text embeddings: %w", err)
	}

	return srv, nil
}

func (s *InferenceServer) computeTextEmbeds(ctx context.Context) error {
	nPrompts := len(s.inputIDs)

	zeroPV := make([][][][]float32, nPrompts)
	for i := range zeroPV {
		pv := make([][][]float32, 3)
		for c := 0; c < 3; c++ {
			pv[c] = make([][]float32, 512)
			for y := 0; y < 512; y++ {
				pv[c][y] = make([]float32, 512)
			}
		}
		zeroPV[i] = pv
	}

	textEmbeds, err := s.triton.InferTextEmbeds(ctx, s.inputIDs, zeroPV)
	if err != nil {
		return fmt.Errorf("triton text embeds: %w", err)
	}

	if len(textEmbeds) != nPrompts*1152 {
		return fmt.Errorf("expected %d values, got %d", nPrompts*1152, len(textEmbeds))
	}

	embeds := make([][]float32, nPrompts)
	for i := range embeds {
		embeds[i] = textEmbeds[i*1152 : (i+1)*1152]
	}

	s.textEmbeds = embeds
	return nil
}

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

	return s.computeTextEmbeds(context.Background())
}

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
			return nil, connect.NewError(connect.CodeInvalidArgument, fmt.Errorf("decode image: %w", err))
		}
		pixelValues = [][][][]float32{pv}
	case *v1.ClassifyRequest_Video:
		if len(content.Video.Frames) == 0 {
			return nil, connect.NewError(connect.CodeInvalidArgument, fmt.Errorf("video has no frames"))
		}
		for _, frame := range content.Video.Frames {
			pv, err := decodeJPEG(frame)
			if err != nil {
				return nil, connect.NewError(connect.CodeInvalidArgument, fmt.Errorf("decode frame: %w", err))
			}
			pixelValues = append(pixelValues, pv)
		}
	default:
		return nil, connect.NewError(connect.CodeInvalidArgument, fmt.Errorf("no content provided"))
	}

	nImages := len(pixelValues)
	seqLen := len(s.inputIDs[0])
	zeroIDs := make([][]int64, nImages)
	for i := range zeroIDs {
		zeroIDs[i] = make([]int64, seqLen)
	}

	s.mu.RLock()
	textEmbeds := s.textEmbeds
	cats := s.categories
	cfg := s.config
	s.mu.RUnlock()

	imageEmbedsFlat, err := s.triton.InferImageEmbeds(ctx, zeroIDs, pixelValues)
	if err != nil {
		return nil, connect.NewError(connect.CodeUnavailable, fmt.Errorf("triton image embeds: %w", err))
	}

	imageEmbeds := make([][]float32, nImages)
	for i := range imageEmbeds {
		imageEmbeds[i] = imageEmbedsFlat[i*1152 : (i+1)*1152]
	}

	logitScale := float32(cfg.Classifier.LogitScale)
	logitBias := float32(cfg.Classifier.LogitBias)

	aggScores := make(map[string]float32)

	for _, imgEmb := range imageEmbeds {
		nPrompts := len(textEmbeds)
		logits := make([]float32, nPrompts)
		for j := 0; j < nPrompts; j++ {
			cosSim := cosineSimilarity(imgEmb, textEmbeds[j])
			logits[j] = logitScale*cosSim + logitBias
		}

		result := classifier.Classify(logits, cats)
		for cat, score := range result.Scores {
			if float32(score) > aggScores[cat] {
				aggScores[cat] = float32(score)
			}
		}
	}

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

	if video, ok := req.Msg.Content.(*v1.ClassifyRequest_Video); ok {
		resp.IsPartialScan = video.Video.IsPartialScan
	}

	return connect.NewResponse(resp), nil
}

func (s *InferenceServer) Health(
	ctx context.Context,
	req *connect.Request[v1.HealthRequest],
) (*connect.Response[v1.HealthResponse], error) {
	ready, err := s.triton.Health(ctx)
	if err != nil {
		return nil, connect.NewError(connect.CodeUnavailable, fmt.Errorf("triton health: %w", err))
	}

	s.mu.RLock()
	loadedAt := s.loadedAt
	version := s.config.Model.Instance.Version
	s.mu.RUnlock()

	return connect.NewResponse(&v1.HealthResponse{
		Ready:        ready,
		ModelVersion: version,
		LoadedAtUnix: loadedAt.Unix(),
	}), nil
}

func (s *InferenceServer) TritonHealth(ctx context.Context) (bool, error) {
	return s.triton.Health(ctx)
}

func cosineSimilarity(a, b []float32) float32 {
	var dot, normA, normB float32
	for i := range a {
		dot += a[i] * b[i]
		normA += a[i] * a[i]
		normB += b[i] * b[i]
	}
	if normA == 0 || normB == 0 {
		return 0
	}
	return dot / float32(math.Sqrt(float64(normA)*float64(normB)))
}

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
