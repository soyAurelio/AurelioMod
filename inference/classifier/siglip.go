// SigLIP2 classifier: sigmoid, prompt ensembling, and thresholding.
// Takes raw logits from Triton and produces content moderation scores.

package classifier

import "math"

// CategoryResult holds classification output for a single category.
type CategoryResult struct {
	Name      string
	Score     float64
	Triggered bool
	Threshold float64
}

// ClassifyResult holds the full classification output.
type ClassifyResult struct {
	Scores      map[string]float64
	TopCategory string
	Confidence  float64
	Triggered   []string
}

// Category defines a content category with pre-computed input_ids.
type Category struct {
	Name       string
	Threshold  float64
	NumPrompts int // number of prompts (and input_ids) for this category
}

// Classify applies sigmoid to logits, ensembles prompts per category (max),
// and returns triggered categories.
//
// logits shape: [image_batch_size, total_prompts]
// categories define the prompt boundaries: first N prompts belong to cat[0], etc.
func Classify(logits []float32, categories []Category) ClassifyResult {
	scores := make(map[string]float64)
	var triggered []string
	topCat := ""
	topScore := 0.0

	offset := 0
	for _, cat := range categories {
		// Max aggregation across all prompts in this category
		maxProb := 0.0
		for i := 0; i < cat.NumPrompts; i++ {
			logit := float64(logits[offset+i])
			prob := sigmoid(logit)
			if prob > maxProb {
				maxProb = prob
			}
		}
		offset += cat.NumPrompts

		scores[cat.Name] = maxProb
		if maxProb >= cat.Threshold {
			triggered = append(triggered, cat.Name)
		}
		if maxProb > topScore {
			topScore = maxProb
			topCat = cat.Name
		}
	}

	return ClassifyResult{
		Scores:      scores,
		TopCategory: topCat,
		Confidence:  topScore,
		Triggered:   triggered,
	}
}

// sigmoid computes 1 / (1 + exp(-x)).
func sigmoid(x float64) float64 {
	return 1.0 / (1.0 + math.Exp(-x))
}
