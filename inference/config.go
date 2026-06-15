// Inference Service configuration with pre-computed input_ids.
// Hot-reloaded via SIGHUP. input_ids are pre-computed at build time
// using scripts/precompute_input_ids.py — no runtime tokenizer needed.
//
// Architecture:
//   Engine → ConnectRPC → Inference Service (Go)
//                       → gRPC → Triton (GPU, unified SigLIP2 ONNX)
//
// Triton receives input_ids (pre-computed) + pixel_values (images),
// returns logits_per_image directly. Sigmoid + thresholding in Go.

package inference

import (
	"encoding/json"
	"fmt"
	"os"
	"time"

	"gopkg.in/yaml.v3"
)

// Config holds the full inference service configuration.
type Config struct {
	Model      ModelConfig      `yaml:"model"`
	Triton     TritonConfig     `yaml:"triton"`
	Classifier ClassifierConfig `yaml:"classifier"`
	Video      VideoConfig      `yaml:"video"`
	Circuit    CircuitConfig    `yaml:"circuit_breaker"`
}

// ModelConfig identifies the SigLIP2 model and tokenizer.
type ModelConfig struct {
	Instance struct {
		Name            string `yaml:"name"`
		HFID            string `yaml:"hf_id"`
		Resolution      int    `yaml:"resolution"`
		TritonModelName string `yaml:"triton_model_name"`
		Version         string `yaml:"version"`
	} `yaml:"instance"`
	TokenizerPath      string `yaml:"tokenizer_path"`
	TokenizerMaxLength int    `yaml:"tokenizer_max_length"`
}

// TritonConfig holds gRPC connection and batching settings.
type TritonConfig struct {
	Endpoint        string `yaml:"endpoint"`
	DynamicBatching struct {
		MaxQueueDelayMs    int   `yaml:"max_queue_delay_ms"`
		PreferredBatchSize []int `yaml:"preferred_batch_size"`
		MaxBatchSize       int   `yaml:"max_batch_size"`
	} `yaml:"dynamic_batching"`
}

// ClassifierConfig holds logit parameters, categories, and pre-computed input_ids.
type ClassifierConfig struct {
	LogitScale float64          `yaml:"logit_scale"`
	LogitBias  float64          `yaml:"logit_bias"`
	Categories []CategoryConfig `yaml:"categories"`
}

// CategoryConfig defines a content category with prompts and pre-computed token IDs.
type CategoryConfig struct {
	Name        string   `yaml:"name"`
	Threshold   float64  `yaml:"threshold"`
	Prompts     []string `yaml:"prompts"`
	Aggregation string   `yaml:"aggregation"` // "max"
	Action      string   `yaml:"action"`      // "block"
	// Pre-computed input_ids for each prompt. Filled from input_ids.json at build time.
	InputIDs [][]int64 `yaml:"input_ids"`
}

// VideoConfig holds video processing tier limits.
type VideoConfig struct {
	Tiers map[string]struct {
		MaxFrames          int     `yaml:"max_frames"`
		FramesPerSecond    float64 `yaml:"frames_per_second"`
		TrustedScanSeconds int     `yaml:"trusted_scan_seconds"`
	} `yaml:"tiers"`
	CacheFilter      bool   `yaml:"cache_filter"`
	EarlyExit        bool   `yaml:"early_exit"`
	SamplingStrategy string `yaml:"sampling_strategy"`
}

// CircuitConfig holds gRPC circuit breaker settings.
type CircuitConfig struct {
	FailureThreshold          int           `yaml:"failure_threshold"`
	FailureRateThreshold      float64       `yaml:"failure_rate_threshold"`
	ConsecutiveSuccessToClose int           `yaml:"consecutive_success_to_close"`
	Timeout                   time.Duration `yaml:"timeout"`
	HalfOpenMaxCalls          int           `yaml:"half_open_max_calls"`
}

// InputIDsFile holds the pre-computed token IDs from scripts/precompute_input_ids.py.
type InputIDsFile struct {
	ModelID        string               `json:"model_id"`
	TotalPrompts   int                  `json:"total_prompts"`
	MaxTokenLength int                  `json:"max_token_length"`
	Categories     map[string][][]int64 `json:"categories"`
}

// LoadConfig reads and parses the YAML configuration file.
func LoadConfig(path string) (*Config, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read config %s: %w", path, err)
	}
	var cfg Config
	if err := yaml.Unmarshal(data, &cfg); err != nil {
		return nil, fmt.Errorf("parse config %s: %w", path, err)
	}
	return &cfg, nil
}

// LoadInputIDs reads the pre-computed input_ids JSON file and populates
// the config's categories with their token IDs.
func (c *Config) LoadInputIDs(path string) error {
	data, err := os.ReadFile(path)
	if err != nil {
		return fmt.Errorf("read input_ids %s: %w", path, err)
	}

	var idsFile InputIDsFile
	if err := json.Unmarshal(data, &idsFile); err != nil {
		return fmt.Errorf("parse input_ids %s: %w", path, err)
	}

	for i := range c.Classifier.Categories {
		catName := c.Classifier.Categories[i].Name
		ids, ok := idsFile.Categories[catName]
		if !ok {
			return fmt.Errorf("category %q not found in input_ids", catName)
		}
		c.Classifier.Categories[i].InputIDs = ids
	}

	if err := c.validate(); err != nil {
		return fmt.Errorf("validate config with input_ids: %w", err)
	}
	return nil
}

func (c *Config) validate() error {
	if c.Triton.Endpoint == "" {
		return fmt.Errorf("triton.endpoint is required")
	}
	if len(c.Classifier.Categories) == 0 {
		return fmt.Errorf("classifier.categories must not be empty")
	}
	for _, cat := range c.Classifier.Categories {
		if len(cat.InputIDs) == 0 {
			return fmt.Errorf("category %q has no input_ids (run scripts/precompute_input_ids.py)", cat.Name)
		}
		if len(cat.InputIDs) != len(cat.Prompts) {
			return fmt.Errorf("category %q: input_ids count (%d) != prompts count (%d)",
				cat.Name, len(cat.InputIDs), len(cat.Prompts))
		}
	}
	return nil
}

// TotalPrompts returns the total number of prompts across all categories.
func (c *ClassifierConfig) TotalPrompts() int {
	n := 0
	for _, cat := range c.Categories {
		n += len(cat.Prompts)
	}
	return n
}

// FlattenInputIDs returns all input_ids concatenated for a single Triton call.
// Returns the flattened tensor [total_prompts, max_seq_len] and the max sequence length.
// Shorter sequences are padded with tokenizer pad_token_id (0).
func (c *ClassifierConfig) FlattenInputIDs() ([][]int64, int) {
	var allIDs [][]int64
	maxLen := 0
	for _, cat := range c.Categories {
		for _, ids := range cat.InputIDs {
			if len(ids) > maxLen {
				maxLen = len(ids)
			}
			allIDs = append(allIDs, ids)
		}
	}
	// Pad to max length
	for i := range allIDs {
		for len(allIDs[i]) < maxLen {
			allIDs[i] = append(allIDs[i], 0) // pad_token_id = 0 for SigLIP2
		}
	}
	return allIDs, maxLen
}
