package snapshot

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/everstacklabs/everstack/internal/storage"
)

// memBlob is an in-memory storage.BlobPlane used by the workspace store
// tests. Multipart completion re-reads the assembled object and checks the
// declared SHA-256 and size, mirroring the s3 adapter's verify step.
type memBlob struct {
	mu      sync.Mutex
	objects map[string][]byte // bucket + "\x00" + key
	sums    map[string]string
	parts   map[string]map[int32][]byte
	nextID  int

	// Fault injection.
	corruptOnComplete bool // flip one byte after upload, before verify
	failUploadPart    error
	corruptOnGet      map[string]bool // key -> serve flipped bytes on Get
	deleted           []string
}

func newMemBlob() *memBlob {
	return &memBlob{
		objects:      map[string][]byte{},
		sums:         map[string]string{},
		parts:        map[string]map[int32][]byte{},
		corruptOnGet: map[string]bool{},
	}
}

func objID(bucket, key string) string { return bucket + "\x00" + key }

func (m *memBlob) put(bucket, key string, data []byte, sum string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.objects[objID(bucket, key)] = append([]byte(nil), data...)
	m.sums[objID(bucket, key)] = sum
}

func (m *memBlob) has(bucket, key string) bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	_, ok := m.objects[objID(bucket, key)]
	return ok
}

func (m *memBlob) keys(bucket string) []string {
	m.mu.Lock()
	defer m.mu.Unlock()
	var out []string
	for id := range m.objects {
		if strings.HasPrefix(id, bucket+"\x00") {
			out = append(out, strings.TrimPrefix(id, bucket+"\x00"))
		}
	}
	sort.Strings(out)
	return out
}

func notFound(op string) error {
	return storage.NewOperationError(op, storage.ErrorNotFound, false, "NoSuchKey", 404)
}

func (m *memBlob) PutPresignedURL(context.Context, string, string, string, int64, time.Duration) (string, map[string]string, error) {
	return "", nil, fmt.Errorf("presign not supported in memBlob")
}
func (m *memBlob) GetPresignedURL(context.Context, string, string, time.Duration) (string, error) {
	return "", fmt.Errorf("presign not supported in memBlob")
}
func (m *memBlob) Put(_ context.Context, bucket, key, _ string, body io.Reader) (string, error) {
	data, err := io.ReadAll(body)
	if err != nil {
		return "", err
	}
	m.put(bucket, key, data, "")
	return "etag", nil
}
func (m *memBlob) Delete(_ context.Context, bucket, key string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	delete(m.objects, objID(bucket, key))
	delete(m.sums, objID(bucket, key))
	m.deleted = append(m.deleted, key)
	return nil
}
func (m *memBlob) Head(_ context.Context, bucket, key string) (int64, string, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	data, ok := m.objects[objID(bucket, key)]
	if !ok {
		return 0, "", notFound("head object")
	}
	return int64(len(data)), "", nil
}
func (m *memBlob) List(_ context.Context, bucket, prefix string) ([]storage.BucketObject, error) {
	var out []storage.BucketObject
	for _, k := range m.keys(bucket) {
		if strings.HasPrefix(k, prefix) {
			m.mu.Lock()
			size := int64(len(m.objects[objID(bucket, k)]))
			m.mu.Unlock()
			out = append(out, storage.BucketObject{Key: k, SizeBytes: size})
		}
	}
	return out, nil
}
func (m *memBlob) BlobCapabilities() storage.BlobCapabilities {
	return storage.BlobCapabilities{ContractVersion: storage.BlobPlaneV2, DirectRead: true, Multipart: true, SHA256: true}
}

type verifyingReader struct {
	r        io.Reader
	h        hashWriterSum
	expected string
}

type hashWriterSum interface {
	io.Writer
	Sum([]byte) []byte
}

func (v *verifyingReader) Read(p []byte) (int, error) {
	n, err := v.r.Read(p)
	if n > 0 {
		_, _ = v.h.Write(p[:n])
	}
	if err == io.EOF && v.expected != "" && hex.EncodeToString(v.h.Sum(nil)) != v.expected {
		return n, storage.NewOperationError("read object", storage.ErrorChecksumMismatch, false, "DigestMismatch", 0)
	}
	return n, err
}

func (m *memBlob) Get(_ context.Context, bucket, key string) (*storage.ObjectReader, error) {
	m.mu.Lock()
	data, ok := m.objects[objID(bucket, key)]
	sum := m.sums[objID(bucket, key)]
	corrupt := m.corruptOnGet[key]
	m.mu.Unlock()
	if !ok {
		return nil, notFound("get object")
	}
	served := append([]byte(nil), data...)
	if corrupt && len(served) > 0 {
		served[len(served)/2] ^= 0xff
	}
	info := storage.ObjectInfo{Key: key, SizeBytes: int64(len(data))}
	if sum != "" {
		info.Checksum = storage.Checksum{Algorithm: storage.ChecksumSHA256, Value: sum}
	}
	body := io.NopCloser(&verifyingReader{r: bytes.NewReader(served), h: sha256.New(), expected: sum})
	return &storage.ObjectReader{ObjectInfo: info, Body: body}, nil
}
func (m *memBlob) ListPage(context.Context, string, storage.ListOptions) (storage.ObjectPage, error) {
	return storage.ObjectPage{}, fmt.Errorf("not implemented")
}
func (m *memBlob) PutIfAbsent(context.Context, string, string, io.Reader, storage.PutOptions) (storage.ObjectInfo, error) {
	return storage.ObjectInfo{}, fmt.Errorf("not implemented")
}
func (m *memBlob) CopyIfAbsent(context.Context, string, string, string, string) (storage.CopyResult, error) {
	return storage.CopyResult{}, fmt.Errorf("not implemented")
}
func (m *memBlob) BeginMultipart(_ context.Context, bucket, key string, options storage.MultipartOptions) (storage.MultipartUpload, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.nextID++
	id := fmt.Sprintf("up-%d", m.nextID)
	m.parts[id] = map[int32][]byte{}
	return storage.MultipartUpload{Bucket: bucket, Key: key, ID: id, Options: options}, nil
}
func (m *memBlob) UploadPart(_ context.Context, upload storage.MultipartUpload, partNumber int32, body io.Reader, _ storage.PutOptions) (storage.UploadedPart, error) {
	if m.failUploadPart != nil {
		return storage.UploadedPart{}, m.failUploadPart
	}
	data, err := io.ReadAll(body)
	if err != nil {
		return storage.UploadedPart{}, err
	}
	m.mu.Lock()
	m.parts[upload.ID][partNumber] = data
	m.mu.Unlock()
	return storage.UploadedPart{Number: partNumber, ETag: "p", Checksum: storage.NewSHA256Checksum(data)}, nil
}
func (m *memBlob) CompleteMultipart(_ context.Context, upload storage.MultipartUpload, parts []storage.UploadedPart) (storage.ObjectInfo, error) {
	m.mu.Lock()
	var buf bytes.Buffer
	for i := range parts {
		buf.Write(m.parts[upload.ID][int32(i+1)])
	}
	delete(m.parts, upload.ID)
	m.mu.Unlock()
	data := buf.Bytes()
	if m.corruptOnComplete && len(data) > 0 {
		data[0] ^= 0xff
	}
	m.put(upload.Bucket, upload.Key, data, "")
	// Verify like the s3 adapter: full re-read against the declared digest.
	sum := sha256.Sum256(data)
	actual := hex.EncodeToString(sum[:])
	if !upload.Options.Checksum.IsZero() && actual != upload.Options.Checksum.Value {
		_ = m.Delete(context.Background(), upload.Bucket, upload.Key)
		return storage.ObjectInfo{}, storage.NewOperationError("verify multipart upload", storage.ErrorChecksumMismatch, false, "DigestMismatch", 0)
	}
	m.mu.Lock()
	m.sums[objID(upload.Bucket, upload.Key)] = upload.Options.Checksum.Value
	m.mu.Unlock()
	return storage.ObjectInfo{Key: upload.Key, SizeBytes: int64(len(data)), Checksum: storage.Checksum{Algorithm: storage.ChecksumSHA256, Value: actual}}, nil
}
func (m *memBlob) AbortMultipart(_ context.Context, upload storage.MultipartUpload) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	delete(m.parts, upload.ID)
	return nil
}

var _ storage.BlobPlane = (*memBlob)(nil)
