package tests

import (
	"bytes"
	"crypto/rand"
	"encoding/csv"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"strconv"
	"testing"
	"time"

	"github.com/blaxel-ai/sandbox-api/integration_tests/common"
	"github.com/blaxel-ai/sandbox-api/src/handler"
	"github.com/stretchr/testify/require"
)

// generateRandomContent generates random text content of a given size
func generateRandomContent(size int) string {
	const charset = "abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789 \n"
	b := make([]byte, size)
	rand.Read(b)
	for i := range b {
		b[i] = charset[int(b[i])%len(charset)]
	}
	return string(b)
}

// uploadViaJSON uploads a file using application/json
func uploadViaJSON(path, content string) (time.Duration, error) {
	start := time.Now()

	request := map[string]interface{}{
		"content": content,
	}

	var successResp handler.SuccessResponse
	resp, err := common.MakeRequestAndParse(http.MethodPut, common.EncodeFilesystemPath(path), request, &successResp)
	if err != nil {
		return 0, err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return 0, fmt.Errorf("unexpected status code: %d", resp.StatusCode)
	}

	return time.Since(start), nil
}

// uploadViaMultipart uploads a file using multipart/form-data
func uploadViaMultipart(path, content string) (time.Duration, error) {
	start := time.Now()
	resp, err := common.MakeMultipartRequestStream(http.MethodPut, common.EncodeFilesystemPath(path), bytes.NewReader([]byte(content)), "upload", nil)
	if err != nil {
		return 0, err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		bodyBytes, _ := io.ReadAll(resp.Body)
		return 0, fmt.Errorf("unexpected status code: %d, body: %s", resp.StatusCode, string(bodyBytes))
	}

	return time.Since(start), nil
}

// TestUploadPerformanceComparison benchmarks JSON vs multipart uploads
func TestUploadPerformanceComparison(t *testing.T) {
	// Test with different file sizes
	testCases := []struct {
		name string
		size int
	}{
		{"10B", 10},
		{"100B", 100},
		{"1KB", 1024},
		{"10KB", 10 * 1024},
		{"100KB", 100 * 1024},
	}

	// Number of iterations per size
	iterations := 5

	fmt.Println("\n=== Upload Performance Comparison: JSON vs Multipart ===")
	fmt.Printf("%-10s | %-20s | %-20s | %-10s\n", "Size", "JSON (avg)", "Multipart (avg)", "Speedup")
	fmt.Println("-----------|----------------------|----------------------|-----------")

	// Collect rows for CSV report
	var reportRows [][]string
	reportRows = append(reportRows, []string{"size", "json_avg_ns", "multipart_avg_ns", "speedup"})

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			content := generateRandomContent(tc.size)

			var jsonTotal, multipartTotal time.Duration

			// Test JSON uploads
			for i := 0; i < iterations; i++ {
				path := fmt.Sprintf("/tmp/test-json-%s-%d-%d", tc.name, time.Now().UnixNano(), i)
				duration, err := uploadViaJSON(path, content)
				require.NoError(t, err, "JSON upload failed for size %s iteration %d", tc.name, i)
				jsonTotal += duration

				// Clean up
				_, _ = common.MakeRequest(http.MethodDelete, common.EncodeFilesystemPath(path), nil)
			}

			// Test multipart uploads
			for i := 0; i < iterations; i++ {
				path := fmt.Sprintf("/tmp/test-multipart-%s-%d-%d", tc.name, time.Now().UnixNano(), i)
				duration, err := uploadViaMultipart(path, content)
				require.NoError(t, err, "Multipart upload failed for size %s iteration %d", tc.name, i)
				multipartTotal += duration

				// Clean up
				_, _ = common.MakeRequest(http.MethodDelete, common.EncodeFilesystemPath(path), nil)
			}

			jsonAvg := jsonTotal / time.Duration(iterations)
			multipartAvg := multipartTotal / time.Duration(iterations)
			speedup := float64(jsonAvg) / float64(multipartAvg)

			fmt.Printf("%-10s | %-20s | %-20s | %.2fx\n",
				tc.name,
				jsonAvg.String(),
				multipartAvg.String(),
				speedup)

			// Add to CSV rows
			reportRows = append(reportRows, []string{
				tc.name,
				strconv.FormatInt(jsonAvg.Nanoseconds(), 10),
				strconv.FormatInt(multipartAvg.Nanoseconds(), 10),
				fmt.Sprintf("%.4f", speedup),
			})
		})
	}

	fmt.Println("===========================================================")

	// Write CSV report
	_ = os.MkdirAll("reports", 0755)
	f, err := os.Create("reports/perf_comparison.csv")
	require.NoError(t, err)
	defer f.Close()
	cw := csv.NewWriter(f)
	_ = cw.WriteAll(reportRows)
	cw.Flush()

	// Write simple HTML table report
	html := "<html><head><meta charset=\"utf-8\"><title>Upload Comparison</title></head><body>" +
		"<h3>Upload Performance Comparison</h3><table border=\"1\" cellspacing=\"0\" cellpadding=\"4\"><tr><th>Size</th><th>JSON (avg ns)</th><th>Multipart (avg ns)</th><th>Speedup</th></tr>"
	for i, row := range reportRows {
		if i == 0 {
			continue
		}
		html += fmt.Sprintf("<tr><td>%s</td><td>%s</td><td>%s</td><td>%s</td></tr>", row[0], row[1], row[2], row[3])
	}
	html += "</table></body></html>"
	_ = os.WriteFile("reports/perf_comparison.html", []byte(html), 0644)
}

// BenchmarkUploadJSON benchmarks JSON uploads
func BenchmarkUploadJSON(b *testing.B) {
	sizes := []int{10, 100, 1024, 10 * 1024, 100 * 1024, 1024 * 1024, 5 * 1024 * 1024}

	for _, size := range sizes {
		content := generateRandomContent(size)
		b.Run(fmt.Sprintf("%dB", size), func(b *testing.B) {
			b.SetBytes(int64(size))
			b.ReportAllocs()
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				path := fmt.Sprintf("/tmp/bench-json-%d-%d", time.Now().UnixNano(), i)
				_, err := uploadViaJSON(path, content)
				if err != nil {
					b.Fatal(err)
				}
				// Clean up
				_, _ = common.MakeRequest(http.MethodDelete, common.EncodeFilesystemPath(path), nil)
			}
		})
	}
}

// BenchmarkUploadMultipart benchmarks multipart uploads
func BenchmarkUploadMultipart(b *testing.B) {
	sizes := []int{10, 100, 1024, 10 * 1024, 100 * 1024, 1024 * 1024, 5 * 1024 * 1024}

	for _, size := range sizes {
		content := generateRandomContent(size)
		b.Run(fmt.Sprintf("%dB", size), func(b *testing.B) {
			b.SetBytes(int64(size))
			b.ReportAllocs()
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				path := fmt.Sprintf("/tmp/bench-multipart-%d-%d", time.Now().UnixNano(), i)
				_, err := uploadViaMultipart(path, content)
				if err != nil {
					b.Fatal(err)
				}
				// Clean up
				_, _ = common.MakeRequest(http.MethodDelete, common.EncodeFilesystemPath(path), nil)
			}
		})
	}
}

// TestMultipartBackwardCompatibility verifies that both JSON and multipart work correctly
func TestMultipartBackwardCompatibility(t *testing.T) {
	content := "Hello, World! This is a test."
	jsonPath := fmt.Sprintf("/tmp/test-compat-json-%d", time.Now().UnixNano())
	multipartPath := fmt.Sprintf("/tmp/test-compat-multipart-%d", time.Now().UnixNano())

	// Test JSON upload
	t.Run("JSON Upload", func(t *testing.T) {
		_, err := uploadViaJSON(jsonPath, content)
		require.NoError(t, err, "JSON upload should work")

		// Verify file was created correctly
		resp, err := common.MakeRequest(http.MethodGet, common.EncodeFilesystemPath(jsonPath), nil)
		require.NoError(t, err)
		defer resp.Body.Close()

		var fileResponse struct {
			Content string `json:"content"`
		}
		err = json.NewDecoder(resp.Body).Decode(&fileResponse)
		require.NoError(t, err)
		require.Equal(t, content, fileResponse.Content, "Content should match")
	})

	// Test multipart upload
	t.Run("Multipart Upload", func(t *testing.T) {
		_, err := uploadViaMultipart(multipartPath, content)
		require.NoError(t, err, "Multipart upload should work")

		// Verify file was created correctly
		resp, err := common.MakeRequest(http.MethodGet, common.EncodeFilesystemPath(multipartPath), nil)
		require.NoError(t, err)
		defer resp.Body.Close()

		var fileResponse struct {
			Content string `json:"content"`
		}
		err = json.NewDecoder(resp.Body).Decode(&fileResponse)
		require.NoError(t, err)
		require.Equal(t, content, fileResponse.Content, "Content should match")
	})

	// Clean up
	_, _ = common.MakeRequest(http.MethodDelete, common.EncodeFilesystemPath(jsonPath), nil)
	_, _ = common.MakeRequest(http.MethodDelete, common.EncodeFilesystemPath(multipartPath), nil)
}

// TestMultipartStreamingLargeFile validates streaming multipart upload for large file
func TestMultipartStreamingLargeFile(t *testing.T) {
	size := 5 * 1024 * 1024
	content := generateRandomContent(size)
	path := fmt.Sprintf("/tmp/test-stream-multipart-%d", time.Now().UnixNano())

	// Upload via streaming multipart
	resp, err := common.MakeMultipartRequestStream(http.MethodPut, common.EncodeFilesystemPath(path), bytes.NewReader([]byte(content)), "upload", map[string]string{"permissions": "0644"})
	require.NoError(t, err)
	defer resp.Body.Close()
	require.Equal(t, http.StatusOK, resp.StatusCode)

	// Fetch file content back and compare
	var fileResponse struct {
		Content string `json:"content"`
	}
	resp, err = common.MakeRequestAndParse(http.MethodGet, common.EncodeFilesystemPath(path), nil, &fileResponse)
	require.NoError(t, err)
	defer resp.Body.Close()
	require.Equal(t, content, fileResponse.Content, "content should match exactly")

	// Cleanup
	_, _ = common.MakeRequest(http.MethodDelete, common.EncodeFilesystemPath(path), nil)
}
