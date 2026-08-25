package archive

import (
	"context"
	"encoding/xml"
	"fmt"
	"io"
	"net/http"
	"strings"
)

// minPartSize is the smallest part S3 accepts for anything but the last part.
// A caller presigning smaller parts would only find out when the upload is
// completed, with the whole archive already transferred.
const minPartSize = 5 << 20

// MultipartUpload carries a multipart upload the caller has already created on
// the storage, part by part. The sandbox holds no credentials, so it can neither
// create the upload nor sign a part: it is handed presigned URLs and uses them
// in order.
//
// It is how an archive larger than the 5 GB a single PUT accepts is uploaded,
// and the caller has to presign enough parts for the archive it is about to
// receive: the sandbox only learns the exact size once the filesystem is
// scanned, by which point nothing can be signed any more.
type MultipartUpload struct {
	// PartURLs are presigned PUT URLs, one per part, in order. Extra ones are
	// left unused.
	PartURLs []string `json:"partUrls"`
	// PartSize is the number of bytes sent to every part but the last.
	PartSize int64 `json:"partSize" example:"536870912"`
	// CompleteURL is a presigned POST URL that assembles the parts.
	CompleteURL string `json:"completeUrl"`
	// AbortURL is a presigned DELETE URL that discards the parts already
	// uploaded. Without it a failed export leaves them on the storage until a
	// lifecycle rule removes them.
	AbortURL string `json:"abortUrl,omitempty"`
} // @name MultipartUpload

func (m *MultipartUpload) validate() error {
	if m == nil {
		return nil
	}
	if len(m.PartURLs) == 0 {
		return invalidOptions("multipart.partUrls is required")
	}
	if m.PartSize < minPartSize {
		return invalidOptions("multipart.partSize must be at least %d bytes", minPartSize)
	}
	if m.CompleteURL == "" {
		return invalidOptions("multipart.completeUrl is required")
	}
	return nil
}

// parts is how many parts an archive of that size takes, and whether the
// presigned URLs cover it.
func (m *MultipartUpload) parts(size int64) (int, error) {
	count := int((size + m.PartSize - 1) / m.PartSize)
	if count == 0 {
		count = 1
	}
	if count > len(m.PartURLs) {
		return 0, fmt.Errorf("the archive needs %d parts of %d bytes and only %d were presigned", count, m.PartSize, len(m.PartURLs))
	}
	return count, nil
}

// completedPart is one uploaded part, as the completion request names it.
type completedPart struct {
	PartNumber int    `xml:"PartNumber"`
	ETag       string `xml:"ETag"`
}

type completeRequest struct {
	XMLName xml.Name        `xml:"CompleteMultipartUpload"`
	Parts   []completedPart `xml:"Part"`
}

// completeResponse is the completion answer. The storage reports a failure
// there with a 200 and an error document, so the body is read rather than the
// status alone: taking the status for an answer would report an archive as
// uploaded that does not exist.
type completeResponse struct {
	XMLName xml.Name `xml:"CompleteMultipartUploadResult"`
	ETag    string   `xml:"ETag"`
}

// uploadMultipart streams the archive to the presigned parts and assembles them.
//
// The archive is produced once, into a pipe, and cut into parts as it comes: it
// is as large as the sandbox's filesystem, so it is never held anywhere.
func uploadMultipart(ctx context.Context, upload *MultipartUpload, size int64, write func(io.Writer) error) (err error) {
	count, err := upload.parts(size)
	if err != nil {
		return err
	}

	ctx, cancel := context.WithTimeout(ctx, transferTimeout)
	defer cancel()

	reader, writer := io.Pipe()
	go func() {
		// Closing with the error makes the part that is being read fail rather
		// than complete an upload of a truncated archive.
		_ = writer.CloseWithError(write(writer))
	}()
	defer reader.Close()

	defer func() {
		if err != nil {
			abortMultipart(ctx, upload)
		}
	}()

	parts := make([]completedPart, 0, count)
	remaining := size
	for number := 1; number <= count; number++ {
		length := min(remaining, upload.PartSize)
		etag, err := uploadPart(ctx, upload.PartURLs[number-1], length, io.LimitReader(reader, length))
		if err != nil {
			return fmt.Errorf("failed to upload part %d of %d: %w", number, count, err)
		}
		parts = append(parts, completedPart{PartNumber: number, ETag: etag})
		remaining -= length
	}

	return completeMultipart(ctx, upload.CompleteURL, parts)
}

// uploadPart sends one part and returns the entity tag the completion needs.
func uploadPart(ctx context.Context, url string, length int64, body io.Reader) (string, error) {
	request, err := http.NewRequestWithContext(ctx, http.MethodPut, url, body)
	if err != nil {
		return "", fmt.Errorf("failed to build the part request: %w", err)
	}
	request.ContentLength = length

	response, err := transferClient.Do(request)
	if err != nil {
		// The URL is a credential, so it never reaches an error message.
		return "", redactURL(err)
	}
	defer response.Body.Close()

	if response.StatusCode < 200 || response.StatusCode >= 300 {
		message, _ := io.ReadAll(io.LimitReader(response.Body, 2048))
		return "", fmt.Errorf("rejected with status %d: %s", response.StatusCode, string(message))
	}
	// Draining is what lets the connection be reused for the next part, and
	// there are as many parts as the archive is large.
	_, _ = io.Copy(io.Discard, io.LimitReader(response.Body, 4096))

	etag := response.Header.Get("ETag")
	if etag == "" {
		return "", fmt.Errorf("the storage accepted part but returned no entity tag, which the upload cannot be completed without")
	}
	return etag, nil
}

// completeMultipart assembles the parts into the archive object. This is the
// request that makes the object exist, and therefore the one that tells whoever
// watches the bucket that the archive is there.
func completeMultipart(ctx context.Context, url string, parts []completedPart) error {
	body, err := xml.Marshal(completeRequest{Parts: parts})
	if err != nil {
		return fmt.Errorf("failed to serialize the upload completion: %w", err)
	}

	request, err := http.NewRequestWithContext(ctx, http.MethodPost, url, strings.NewReader(string(body)))
	if err != nil {
		return fmt.Errorf("failed to build the completion request: %w", err)
	}
	request.ContentLength = int64(len(body))

	response, err := transferClient.Do(request)
	if err != nil {
		return fmt.Errorf("failed to complete the archive upload: %w", redactURL(err))
	}
	defer response.Body.Close()

	answer, _ := io.ReadAll(io.LimitReader(response.Body, 8192))
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		return fmt.Errorf("archive upload completion rejected with status %d: %s", response.StatusCode, string(answer))
	}
	// A completion that failed halfway answers 200 with an error document, and
	// nothing else says the object was not created.
	var result completeResponse
	if err := xml.Unmarshal(answer, &result); err != nil {
		return fmt.Errorf("the storage did not confirm the archive upload: %s", string(answer))
	}
	return nil
}

// abortMultipart discards the parts of an upload that will not be completed. It
// is best effort: the parts are billed until a lifecycle rule removes them, but
// a failed export has already reported the failure that matters.
func abortMultipart(ctx context.Context, upload *MultipartUpload) {
	if upload.AbortURL == "" {
		return
	}
	// The export's own context may already be done - a timeout is exactly when
	// this runs - so the abort gets one of its own.
	ctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), handshakeTimeout)
	defer cancel()

	request, err := http.NewRequestWithContext(ctx, http.MethodDelete, upload.AbortURL, nil)
	if err != nil {
		return
	}
	response, err := transferClient.Do(request)
	if err != nil {
		return
	}
	defer response.Body.Close()
	_, _ = io.Copy(io.Discard, io.LimitReader(response.Body, 4096))
}
