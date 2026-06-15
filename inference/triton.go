// Triton Inference Server gRPC client.
// Uses Triton's native gRPC protocol (grpc_service.proto) for inference.
//
// Why gRPC over HTTP for production:
//   - Single persistent HTTP/2 connection with multiplexing (no TCP handshake per request)
//   - Binary protobuf serialization avoids JSON overhead for FP32 tensors
//   - No kernel-space CPU contention under high concurrency (HTTP __pv_queued_spin_lock_slowpath)
//   - RawInputContents/RawOutputContents for zero-copy tensor transfer
//   - Connection pooling via gRPC channel (no manual HTTP connection management)

package inference

import (
	"context"
	"encoding/binary"
	"fmt"
	"math"
	"time"

	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/keepalive"

	triton "github.com/soyAurelio/AurelioMod/proto/triton"
)

// TritonClient sends inference requests to a Triton Inference Server via gRPC.
type TritonClient struct {
	conn      *grpc.ClientConn
	client    triton.GRPCInferenceServiceClient
	modelName string
	timeout   time.Duration
}

// NewTritonClient creates a new Triton gRPC client with a persistent connection.
// The gRPC channel uses HTTP/2 multiplexing — all concurrent requests share
// a single TCP connection, avoiding the handshake overhead of HTTP/1.1.
func NewTritonClient(addr, modelName string) (*TritonClient, error) {
	conn, err := grpc.NewClient(
		addr,
		grpc.WithTransportCredentials(insecure.NewCredentials()),
		grpc.WithConnectParams(grpc.ConnectParams{
			MinConnectTimeout: 5 * time.Second,
		}),
		// Keepalive: detect dead connections within 10s. Critical for
		// production where the Inference Service stays up but Triton restarts.
		grpc.WithKeepaliveParams(keepalive.ClientParameters{
			Time:                10 * time.Second,
			Timeout:             5 * time.Second,
			PermitWithoutStream: true,
		}),
		// Default service config: enable retry for transient failures
		grpc.WithDefaultServiceConfig(`{
			"methodConfig": [{
				"name": [{"service": "inference.GRPCInferenceService"}],
				"retryPolicy": {
					"maxAttempts": 3,
					"initialBackoff": "0.05s",
					"maxBackoff": "1s",
					"backoffMultiplier": 2,
					"retryableStatusCodes": ["UNAVAILABLE", "DEADLINE_EXCEEDED"]
				}
			}]
		}`),
	)
	if err != nil {
		return nil, fmt.Errorf("triton grpc dial %s: %w", addr, err)
	}

	return &TritonClient{
		conn:      conn,
		client:    triton.NewGRPCInferenceServiceClient(conn),
		modelName: modelName,
		timeout:   15 * time.Second,
	}, nil
}

// Close shuts down the gRPC connection.
func (c *TritonClient) Close() error {
	return c.conn.Close()
}

// Infer sends an inference request to Triton and returns logits_per_image.
func (c *TritonClient) Infer(
	ctx context.Context,
	inputIDs [][]int64,
	pixelValues [][][][]float32,
) ([]float32, error) {
	return c.inferWithOutput(ctx, inputIDs, pixelValues, "logits_per_image")
}

// InferTextEmbeds sends input_ids with zero pixel_values and returns text_embeds.
func (c *TritonClient) InferTextEmbeds(
	ctx context.Context,
	inputIDs [][]int64,
	pixelValues [][][][]float32,
) ([]float32, error) {
	return c.inferWithOutput(ctx, inputIDs, pixelValues, "text_embeds")
}

// InferImageEmbeds sends pixel_values with zero input_ids and returns image_embeds.
func (c *TritonClient) InferImageEmbeds(
	ctx context.Context,
	inputIDs [][]int64,
	pixelValues [][][][]float32,
) ([]float32, error) {
	return c.inferWithOutput(ctx, inputIDs, pixelValues, "image_embeds")
}

// inferWithOutput sends a request to Triton and extracts the named output tensor.
func (c *TritonClient) inferWithOutput(
	ctx context.Context,
	inputIDs [][]int64,
	pixelValues [][][][]float32,
	outputName string,
) ([]float32, error) {
	ctx, cancel := context.WithTimeout(ctx, c.timeout)
	defer cancel()

	nImages := len(pixelValues)
	nPrompts := len(inputIDs)
	seqLen := len(inputIDs[0])

	// Build raw input contents: binary little-endian tensors.
	// Order matches the inputs array: [pixel_values, input_ids].

	// pixel_values raw: [nImages, 3, 512, 512] FP32 little-endian
	pixelBytes := float32SliceToBytes(flattenFloat32_4D(pixelValues))

	// input_ids raw: [nPrompts, seqLen] INT64 little-endian
	inputIDBytes := int64SliceToBytes(flattenInt64(inputIDs))

	req := &triton.ModelInferRequest{
		ModelName:    c.modelName,
		ModelVersion: "", // use latest
		Inputs: []*triton.ModelInferRequest_InferInputTensor{
			{
				Name:     "pixel_values",
				Datatype: "FP32",
				Shape:    []int64{int64(nImages), 3, 512, 512},
			},
			{
				Name:     "input_ids",
				Datatype: "INT64",
				Shape:    []int64{int64(nPrompts), int64(seqLen)},
			},
		},
		Outputs: []*triton.ModelInferRequest_InferRequestedOutputTensor{
			{Name: outputName},
		},
		RawInputContents: [][]byte{pixelBytes, inputIDBytes},
	}

	resp, err := c.client.ModelInfer(ctx, req)
	if err != nil {
		return nil, fmt.Errorf("triton model infer: %w", err)
	}

	// RawOutputContents[0] is the requested output tensor as FP32 little-endian.
	if len(resp.RawOutputContents) == 0 {
		return nil, fmt.Errorf("triton returned no output contents for %q", outputName)
	}

	result := bytesToFloat32Slice(resp.RawOutputContents[0])
	return result, nil
}

// Health checks if Triton is live and ready via gRPC.
func (c *TritonClient) Health(ctx context.Context) (bool, error) {
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()

	liveResp, err := c.client.ServerLive(ctx, &triton.ServerLiveRequest{})
	if err != nil {
		return false, fmt.Errorf("triton server live: %w", err)
	}
	if !liveResp.Live {
		return false, nil
	}

	readyResp, err := c.client.ServerReady(ctx, &triton.ServerReadyRequest{})
	if err != nil {
		return false, fmt.Errorf("triton server ready: %w", err)
	}
	return readyResp.Ready, nil
}

// --- Binary encoding helpers ---

// float32SliceToBytes encodes []float32 as little-endian bytes.
func float32SliceToBytes(data []float32) []byte {
	buf := make([]byte, len(data)*4)
	for i, v := range data {
		binary.LittleEndian.PutUint32(buf[i*4:], math.Float32bits(v))
	}
	return buf
}

// int64SliceToBytes encodes []int64 as little-endian bytes.
func int64SliceToBytes(data []int64) []byte {
	buf := make([]byte, len(data)*8)
	for i, v := range data {
		binary.LittleEndian.PutUint64(buf[i*8:], uint64(v))
	}
	return buf
}

// bytesToFloat32Slice decodes little-endian bytes to []float32.
func bytesToFloat32Slice(data []byte) []float32 {
	out := make([]float32, len(data)/4)
	for i := range out {
		out[i] = math.Float32frombits(binary.LittleEndian.Uint32(data[i*4:]))
	}
	return out
}

// flattenInt64 converts [][]int64 to []int64 (row-major flatten).
func flattenInt64(m [][]int64) []int64 {
	if len(m) == 0 {
		return nil
	}
	n := len(m) * len(m[0])
	out := make([]int64, 0, n)
	for _, row := range m {
		out = append(out, row...)
	}
	return out
}

// flattenFloat32_4D converts [][][][]float32 to []float32.
func flattenFloat32_4D(m [][][][]float32) []float32 {
	if len(m) == 0 {
		return nil
	}
	total := 0
	for i := range m {
		for j := range m[i] {
			for k := range m[i][j] {
				total += len(m[i][j][k])
			}
		}
	}
	out := make([]float32, 0, total)
	for i := range m {
		for j := range m[i] {
			for k := range m[i][j] {
				out = append(out, m[i][j][k]...)
			}
		}
	}
	return out
}
