package s3

import (
	"context"
	"crypto/md5"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"io"
	"net"
	"net/url"
	"os"
	"sort"
	"strconv"
	"strings"

	"github.com/aws/aws-sdk-go-v2/aws"
	awss3 "github.com/aws/aws-sdk-go-v2/service/s3"
	"github.com/aws/aws-sdk-go-v2/service/s3/types"
	"github.com/aws/smithy-go"
	smithyhttp "github.com/aws/smithy-go/transport/http"
	"github.com/everstacklabs/everstack/internal/storage"
)

const (
	checksumMetadataKey = "everstack-sha256"
	sizeMetadataKey     = "everstack-size"
)

// BlobCapabilities reports what this configured adapter can execute. It is not
// itself evidence that a provider is supported; the conformance profile is the
// release gate for that claim.
func (s *Store) BlobCapabilities() storage.BlobCapabilities {
	return storage.BlobCapabilities{
		ContractVersion:  storage.BlobPlaneV2,
		DirectRead:       true,
		PaginatedList:    true,
		ConditionalWrite: true,
		Copy:             true,
		NativeCopy:       !s.disableNativeCopy,
		SafeCopyFallback: true,
		SafeCopyMaxBytes: s.maxStagingBytes,
		Multipart:        true,
		SHA256:           true,
		PresignedURLs:    true,
		ClassifiedErrors: true,
		Retries:          s.retriesEnabled,
	}
}

func (s *Store) resolvedBucket(bucket string) string {
	if bucket != "" {
		return bucket
	}
	return s.bucket
}

// Get returns a direct streaming read. When SHA-256 metadata is available the
// body verifies the digest at EOF and surfaces a typed checksum error.
func (s *Store) Get(ctx context.Context, bucket, key string) (*storage.ObjectReader, error) {
	result, err := s.client.GetObject(ctx, &awss3.GetObjectInput{
		Bucket:       aws.String(s.resolvedBucket(bucket)),
		Key:          aws.String(s.fullKey(key)),
		ChecksumMode: types.ChecksumModeEnabled,
	})
	if err != nil {
		return nil, s.providerError("get object", err)
	}

	checksum, checksumErr := checksumFromProvider(result.ChecksumSHA256, result.Metadata, result.ChecksumType)
	if checksumErr != nil {
		_ = result.Body.Close()
		return nil, checksumErr
	}
	body := newProviderReadCloser(result.Body, checksum, s.redactor)

	info := storage.ObjectInfo{
		Key:          key,
		ContentType:  aws.ToString(result.ContentType),
		ETag:         trimETag(result.ETag),
		Checksum:     checksum,
		LastModified: aws.ToTime(result.LastModified),
	}
	if result.ContentLength != nil {
		info.SizeBytes = *result.ContentLength
	}
	return &storage.ObjectReader{ObjectInfo: info, Body: body}, nil
}

// ListPage exposes the provider cursor as an opaque token and never performs
// an unbounded scan. The compatibility List method continues to aggregate all
// pages for existing callers.
func (s *Store) ListPage(ctx context.Context, bucket string, options storage.ListOptions) (storage.ObjectPage, error) {
	if options.PageSize < 0 || options.PageSize > 1000 {
		return storage.ObjectPage{}, storage.NewOperationError("list objects", storage.ErrorInvalidArgument, false, "", 0)
	}

	input := &awss3.ListObjectsV2Input{
		Bucket: aws.String(s.resolvedBucket(bucket)),
	}
	if prefix := s.fullKey(options.Prefix); prefix != "" {
		input.Prefix = aws.String(prefix)
	}
	if options.Cursor != "" {
		input.ContinuationToken = aws.String(options.Cursor)
	}
	if options.PageSize > 0 {
		input.MaxKeys = aws.Int32(options.PageSize)
	}

	result, err := s.client.ListObjectsV2(ctx, input)
	if err != nil {
		return storage.ObjectPage{}, s.providerError("list objects", err)
	}

	page := storage.ObjectPage{Objects: make([]storage.ObjectInfo, 0, len(result.Contents))}
	for _, object := range result.Contents {
		key := s.logicalKey(aws.ToString(object.Key))
		info := storage.ObjectInfo{
			Key:          key,
			ETag:         trimETag(object.ETag),
			LastModified: aws.ToTime(object.LastModified),
		}
		if object.Size != nil {
			info.SizeBytes = *object.Size
		}
		page.Objects = append(page.Objects, info)
	}
	if aws.ToBool(result.IsTruncated) {
		page.NextCursor = aws.ToString(result.NextContinuationToken)
		if page.NextCursor == "" {
			return storage.ObjectPage{}, storage.NewOperationError("list objects", storage.ErrorProvider, true, "MissingContinuationToken", 0)
		}
	}
	return page, nil
}

func (s *Store) logicalKey(key string) string {
	if s.pathPrefix != "" && strings.HasPrefix(key, s.pathPrefix+"/") {
		return strings.TrimPrefix(key, s.pathPrefix+"/")
	}
	return key
}

// PutIfAbsent stages and hashes the body before sending it. This makes retry
// replay safe and ensures a declared digest mismatch never reaches a provider.
func (s *Store) PutIfAbsent(ctx context.Context, bucket, key string, body io.Reader, options storage.PutOptions) (storage.ObjectInfo, error) {
	staged, err := stageBody(ctx, body, options, s.maxStagingBytes)
	if err != nil {
		return storage.ObjectInfo{}, err
	}
	defer staged.Close()

	input := &awss3.PutObjectInput{
		Bucket:        aws.String(s.resolvedBucket(bucket)),
		Key:           aws.String(s.fullKey(key)),
		Body:          staged.Reader,
		ContentLength: aws.Int64(staged.SizeBytes),
		ContentType:   optionalString(options.ContentType),
		IfNoneMatch:   aws.String("*"),
		Metadata: map[string]string{
			checksumMetadataKey: staged.Checksum.Value,
			sizeMetadataKey:     strconv.FormatInt(staged.SizeBytes, 10),
		},
	}
	s.applyPutChecksum(input, staged)
	result, err := s.client.PutObject(ctx, input)
	if err != nil {
		return storage.ObjectInfo{}, s.providerError("conditionally put object", err)
	}

	return storage.ObjectInfo{
		Key:         key,
		SizeBytes:   staged.SizeBytes,
		ContentType: options.ContentType,
		ETag:        trimETag(result.ETag),
		Checksum:    staged.Checksum,
	}, nil
}

// CopyIfAbsent uses CopyObject when supported and an explicit direct-read plus
// conditional-create fallback otherwise. Both paths preserve no-overwrite
// semantics; provider errors other than unsupported are never downgraded.
func (s *Store) CopyIfAbsent(ctx context.Context, sourceBucket, sourceKey, destinationBucket, destinationKey string) (storage.CopyResult, error) {
	if s.disableNativeCopy {
		return s.copyFallback(ctx, sourceBucket, sourceKey, destinationBucket, destinationKey)
	}

	source := url.PathEscape(s.resolvedBucket(sourceBucket) + "/" + s.fullKey(sourceKey))
	result, err := s.client.CopyObject(ctx, &awss3.CopyObjectInput{
		Bucket:            aws.String(s.resolvedBucket(destinationBucket)),
		Key:               aws.String(s.fullKey(destinationKey)),
		CopySource:        aws.String(source),
		IfNoneMatch:       aws.String("*"),
		ChecksumAlgorithm: types.ChecksumAlgorithmSha256,
	})
	if err != nil {
		classified := s.providerError("conditionally copy object", err)
		if storage.IsCode(classified, storage.ErrorUnsupported) {
			return s.copyFallback(ctx, sourceBucket, sourceKey, destinationBucket, destinationKey)
		}
		return storage.CopyResult{}, classified
	}

	info, headErr := s.headInfo(ctx, destinationBucket, destinationKey)
	if headErr != nil {
		return storage.CopyResult{}, headErr
	}
	if result.CopyObjectResult != nil && result.CopyObjectResult.ETag != nil {
		info.ETag = trimETag(result.CopyObjectResult.ETag)
	}
	return storage.CopyResult{ObjectInfo: info}, nil
}

func (s *Store) copyFallback(ctx context.Context, sourceBucket, sourceKey, destinationBucket, destinationKey string) (storage.CopyResult, error) {
	source, err := s.Get(ctx, sourceBucket, sourceKey)
	if err != nil {
		return storage.CopyResult{}, err
	}
	defer source.Body.Close()
	if source.SizeBytes > s.maxStagingBytes {
		return storage.CopyResult{}, storage.NewOperationError("copy object", storage.ErrorUnsupported, false, "SafeCopySizeLimit", 0)
	}

	expectedSize := source.SizeBytes
	info, err := s.PutIfAbsent(ctx, destinationBucket, destinationKey, source.Body, storage.PutOptions{
		ContentType:  source.ContentType,
		ExpectedSize: &expectedSize,
		Checksum:     source.Checksum,
	})
	if err != nil {
		return storage.CopyResult{}, err
	}
	return storage.CopyResult{ObjectInfo: info, UsedFallback: true}, nil
}

func (s *Store) BeginMultipart(ctx context.Context, bucket, key string, options storage.MultipartOptions) (storage.MultipartUpload, error) {
	if err := validateMultipartOptions(options); err != nil {
		return storage.MultipartUpload{}, err
	}
	metadata := map[string]string{}
	if !options.Checksum.IsZero() {
		metadata[checksumMetadataKey] = options.Checksum.Value
	}
	if options.ExpectedSize != nil {
		metadata[sizeMetadataKey] = strconv.FormatInt(*options.ExpectedSize, 10)
	}

	input := &awss3.CreateMultipartUploadInput{
		Bucket:      aws.String(s.resolvedBucket(bucket)),
		Key:         aws.String(s.fullKey(key)),
		ContentType: optionalString(options.ContentType),
		Metadata:    metadata,
	}
	if s.wireChecksum == WireChecksumSHA256 {
		input.ChecksumAlgorithm = types.ChecksumAlgorithmSha256
	}
	result, err := s.client.CreateMultipartUpload(ctx, input)
	if err != nil {
		return storage.MultipartUpload{}, s.providerError("begin multipart upload", err)
	}
	if aws.ToString(result.UploadId) == "" {
		return storage.MultipartUpload{}, storage.NewOperationError("begin multipart upload", storage.ErrorProvider, false, "MissingUploadID", 0)
	}
	return storage.MultipartUpload{
		Bucket:  s.resolvedBucket(bucket),
		Key:     key,
		ID:      aws.ToString(result.UploadId),
		Options: options,
	}, nil
}

func (s *Store) UploadPart(ctx context.Context, upload storage.MultipartUpload, partNumber int32, body io.Reader, options storage.PutOptions) (storage.UploadedPart, error) {
	if err := validateUpload(upload); err != nil {
		return storage.UploadedPart{}, err
	}
	if partNumber < 1 || partNumber > 10000 {
		return storage.UploadedPart{}, storage.NewOperationError("upload multipart part", storage.ErrorInvalidArgument, false, "", 0)
	}
	staged, err := stageBody(ctx, body, options, s.maxStagingBytes)
	if err != nil {
		return storage.UploadedPart{}, err
	}
	defer staged.Close()

	input := &awss3.UploadPartInput{
		Bucket:        aws.String(upload.Bucket),
		Key:           aws.String(s.fullKey(upload.Key)),
		UploadId:      aws.String(upload.ID),
		PartNumber:    aws.Int32(partNumber),
		Body:          staged.Reader,
		ContentLength: aws.Int64(staged.SizeBytes),
	}
	s.applyPartChecksum(input, staged)
	result, err := s.client.UploadPart(ctx, input)
	if err != nil {
		return storage.UploadedPart{}, s.providerError("upload multipart part", err)
	}
	return storage.UploadedPart{
		Number:   partNumber,
		ETag:     trimETag(result.ETag),
		Checksum: staged.Checksum,
	}, nil
}

func (s *Store) CompleteMultipart(ctx context.Context, upload storage.MultipartUpload, parts []storage.UploadedPart) (storage.ObjectInfo, error) {
	if err := validateUpload(upload); err != nil {
		return storage.ObjectInfo{}, err
	}
	if len(parts) == 0 {
		return storage.ObjectInfo{}, storage.NewOperationError("complete multipart upload", storage.ErrorInvalidArgument, false, "", 0)
	}
	ordered := append([]storage.UploadedPart(nil), parts...)
	sort.Slice(ordered, func(i, j int) bool { return ordered[i].Number < ordered[j].Number })
	completed := make([]types.CompletedPart, 0, len(ordered))
	for index, part := range ordered {
		if part.Number != int32(index+1) || part.ETag == "" {
			return storage.ObjectInfo{}, storage.NewOperationError("complete multipart upload", storage.ErrorInvalidArgument, false, "", 0)
		}
		if err := part.Checksum.Validate(); err != nil || part.Checksum.IsZero() {
			return storage.ObjectInfo{}, storage.NewOperationError("complete multipart upload", storage.ErrorInvalidArgument, false, "", 0)
		}
		completedPart := types.CompletedPart{
			PartNumber: aws.Int32(part.Number),
			ETag:       aws.String(part.ETag),
		}
		if s.wireChecksum == WireChecksumSHA256 {
			completedPart.ChecksumSHA256 = aws.String(checksumBase64(part.Checksum))
		}
		completed = append(completed, completedPart)
	}

	result, err := s.client.CompleteMultipartUpload(ctx, &awss3.CompleteMultipartUploadInput{
		Bucket:   aws.String(upload.Bucket),
		Key:      aws.String(s.fullKey(upload.Key)),
		UploadId: aws.String(upload.ID),
		MultipartUpload: &types.CompletedMultipartUpload{
			Parts: completed,
		},
	})
	if err != nil {
		return storage.ObjectInfo{}, s.providerError("complete multipart upload", err)
	}

	info := storage.ObjectInfo{
		Key:         upload.Key,
		ContentType: upload.Options.ContentType,
		ETag:        trimETag(result.ETag),
		Checksum:    upload.Options.Checksum,
	}
	if upload.Options.ExpectedSize != nil {
		info.SizeBytes = *upload.Options.ExpectedSize
	}
	if upload.Options.ExpectedSize != nil || !upload.Options.Checksum.IsZero() {
		verified, confirmedMismatch, verifyErr := s.verifyCompletedMultipart(ctx, upload)
		if verifyErr != nil {
			if confirmedMismatch {
				_ = s.deleteCompletedObjectIfMatch(ctx, upload, result.ETag, result.VersionId)
			}
			return storage.ObjectInfo{}, verifyErr
		}
		info = verified
		info.ETag = trimETag(result.ETag)
	}
	return info, nil
}

func (s *Store) AbortMultipart(ctx context.Context, upload storage.MultipartUpload) error {
	if err := validateUpload(upload); err != nil {
		return err
	}
	_, err := s.client.AbortMultipartUpload(ctx, &awss3.AbortMultipartUploadInput{
		Bucket:   aws.String(upload.Bucket),
		Key:      aws.String(s.fullKey(upload.Key)),
		UploadId: aws.String(upload.ID),
	})
	return s.providerError("abort multipart upload", err)
}

func (s *Store) verifyCompletedMultipart(ctx context.Context, upload storage.MultipartUpload) (storage.ObjectInfo, bool, error) {
	object, err := s.Get(ctx, upload.Bucket, upload.Key)
	if err != nil {
		return storage.ObjectInfo{}, false, err
	}
	hasher := sha256.New()
	written, readErr := io.Copy(io.MultiWriter(io.Discard, hasher), object.Body)
	closeErr := object.Body.Close()
	if readErr != nil {
		return storage.ObjectInfo{}, storage.IsCode(readErr, storage.ErrorChecksumMismatch), readErr
	}
	if closeErr != nil {
		return storage.ObjectInfo{}, false, closeErr
	}
	if upload.Options.ExpectedSize != nil && written != *upload.Options.ExpectedSize {
		return storage.ObjectInfo{}, true, storage.NewOperationError("verify multipart upload", storage.ErrorChecksumMismatch, false, "SizeMismatch", 0)
	}
	actual := storage.Checksum{Algorithm: storage.ChecksumSHA256, Value: hex.EncodeToString(hasher.Sum(nil))}
	if !upload.Options.Checksum.IsZero() && actual != upload.Options.Checksum {
		return storage.ObjectInfo{}, true, storage.NewOperationError("verify multipart upload", storage.ErrorChecksumMismatch, false, "DigestMismatch", 0)
	}
	object.SizeBytes = written
	object.Checksum = actual
	return object.ObjectInfo, false, nil
}

// deleteCompletedObjectIfMatch removes only the exact object produced by the
// completed upload. A later writer must never be deleted by verification
// cleanup, and a provider that cannot enforce the condition leaves the object
// in place for explicit repair.
func (s *Store) deleteCompletedObjectIfMatch(ctx context.Context, upload storage.MultipartUpload, etag, versionID *string) error {
	if strings.TrimSpace(aws.ToString(etag)) == "" && strings.TrimSpace(aws.ToString(versionID)) == "" {
		return nil
	}
	input := &awss3.DeleteObjectInput{
		Bucket: aws.String(upload.Bucket),
		Key:    aws.String(s.fullKey(upload.Key)),
	}
	if strings.TrimSpace(aws.ToString(etag)) != "" {
		input.IfMatch = etag
	}
	if strings.TrimSpace(aws.ToString(versionID)) != "" {
		input.VersionId = versionID
	}
	_, err := s.client.DeleteObject(ctx, input)
	return s.providerError("delete invalid multipart object", err)
}

func (s *Store) headInfo(ctx context.Context, bucket, key string) (storage.ObjectInfo, error) {
	result, err := s.client.HeadObject(ctx, &awss3.HeadObjectInput{
		Bucket:       aws.String(s.resolvedBucket(bucket)),
		Key:          aws.String(s.fullKey(key)),
		ChecksumMode: types.ChecksumModeEnabled,
	})
	if err != nil {
		return storage.ObjectInfo{}, s.providerError("head object", err)
	}
	checksum, checksumErr := checksumFromProvider(result.ChecksumSHA256, result.Metadata, result.ChecksumType)
	if checksumErr != nil {
		return storage.ObjectInfo{}, checksumErr
	}
	info := storage.ObjectInfo{
		Key:          key,
		ContentType:  aws.ToString(result.ContentType),
		ETag:         trimETag(result.ETag),
		Checksum:     checksum,
		LastModified: aws.ToTime(result.LastModified),
	}
	if result.ContentLength != nil {
		info.SizeBytes = *result.ContentLength
	}
	return info, nil
}

type stagedUpload struct {
	Reader     *os.File
	SizeBytes  int64
	Checksum   storage.Checksum
	ContentMD5 string
	path       string
}

func stageBody(ctx context.Context, body io.Reader, options storage.PutOptions, maxBytes int64) (*stagedUpload, error) {
	if body == nil {
		return nil, storage.NewOperationError("stage object", storage.ErrorInvalidArgument, false, "", 0)
	}
	if ctx == nil {
		return nil, storage.NewOperationError("stage object", storage.ErrorInvalidArgument, false, "", 0)
	}
	if err := options.Checksum.Validate(); err != nil {
		return nil, err
	}
	if maxBytes <= 0 || maxBytes > maximumStagingBytes {
		return nil, storage.NewOperationError("stage object", storage.ErrorInvalidArgument, false, "StagingLimit", 0)
	}
	readLimit := maxBytes
	if options.ExpectedSize != nil {
		if *options.ExpectedSize < 0 || *options.ExpectedSize > maxBytes {
			return nil, storage.NewOperationError("stage object", storage.ErrorInvalidArgument, false, "SizeLimit", 0)
		}
		readLimit = *options.ExpectedSize
	}
	file, err := os.CreateTemp("", "everstack-storage-upload-*")
	if err != nil {
		return nil, storage.NewOperationError("stage object", storage.ErrorUnavailable, true, "LocalStaging", 0)
	}
	cleanup := func() {
		_ = file.Close()
		_ = os.Remove(file.Name())
	}
	shaHasher := sha256.New()
	md5Hasher := md5.New()
	limited := io.LimitReader(&contextReader{ctx: ctx, inner: body}, readLimit+1)
	size, err := io.Copy(io.MultiWriter(file, shaHasher, md5Hasher), limited)
	if err != nil {
		cleanup()
		var operationErr *storage.OperationError
		if errors.As(err, &operationErr) && operationErr != nil {
			return nil, operationErr
		}
		if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
			return nil, classifyProviderError("stage object", err, nil)
		}
		return nil, storage.NewOperationError("stage object", storage.ErrorUnavailable, false, "BodyRead", 0)
	}
	if size > readLimit {
		cleanup()
		return nil, storage.NewOperationError("stage object", storage.ErrorInvalidArgument, false, "SizeLimit", 0)
	}
	actual := storage.Checksum{Algorithm: storage.ChecksumSHA256, Value: hex.EncodeToString(shaHasher.Sum(nil))}
	if options.ExpectedSize != nil && size != *options.ExpectedSize {
		cleanup()
		return nil, storage.NewOperationError("stage object", storage.ErrorInvalidArgument, false, "SizeMismatch", 0)
	}
	if !options.Checksum.IsZero() && options.Checksum != actual {
		cleanup()
		return nil, storage.NewOperationError("stage object", storage.ErrorChecksumMismatch, false, "DigestMismatch", 0)
	}
	if _, err := file.Seek(0, io.SeekStart); err != nil {
		cleanup()
		return nil, storage.NewOperationError("stage object", storage.ErrorUnavailable, true, "LocalStaging", 0)
	}
	return &stagedUpload{
		Reader: file, SizeBytes: size, Checksum: actual,
		ContentMD5: base64.StdEncoding.EncodeToString(md5Hasher.Sum(nil)), path: file.Name(),
	}, nil
}

type contextReader struct {
	ctx   context.Context
	inner io.Reader
}

func (r *contextReader) Read(buffer []byte) (int, error) {
	select {
	case <-r.ctx.Done():
		return 0, r.ctx.Err()
	default:
	}
	return r.inner.Read(buffer)
}

func (s *Store) applyPutChecksum(input *awss3.PutObjectInput, staged *stagedUpload) {
	if s.wireChecksum == WireChecksumContentMD5 {
		input.ContentMD5 = aws.String(staged.ContentMD5)
		return
	}
	input.ChecksumAlgorithm = types.ChecksumAlgorithmSha256
	input.ChecksumSHA256 = aws.String(checksumBase64(staged.Checksum))
}

func (s *Store) applyPartChecksum(input *awss3.UploadPartInput, staged *stagedUpload) {
	if s.wireChecksum == WireChecksumContentMD5 {
		input.ContentMD5 = aws.String(staged.ContentMD5)
		return
	}
	input.ChecksumSHA256 = aws.String(checksumBase64(staged.Checksum))
}

func (s *stagedUpload) Close() error {
	if s == nil || s.Reader == nil {
		return nil
	}
	closeErr := s.Reader.Close()
	removeErr := os.Remove(s.path)
	if closeErr != nil {
		return closeErr
	}
	if removeErr != nil && !errors.Is(removeErr, os.ErrNotExist) {
		return removeErr
	}
	return nil
}

type providerReadCloser struct {
	inner    io.ReadCloser
	hasher   hashWriter
	expected storage.Checksum
	checked  bool
	redactor *secretRedactor
}

type hashWriter interface {
	Write([]byte) (int, error)
	Sum([]byte) []byte
}

func newProviderReadCloser(inner io.ReadCloser, expected storage.Checksum, redactor *secretRedactor) io.ReadCloser {
	reader := &providerReadCloser{inner: inner, expected: expected, redactor: redactor}
	if !expected.IsZero() {
		reader.hasher = sha256.New()
	}
	return reader
}

func newChecksumReadCloser(inner io.ReadCloser, expected storage.Checksum) io.ReadCloser {
	return newProviderReadCloser(inner, expected, nil)
}

func (r *providerReadCloser) Read(buffer []byte) (int, error) {
	n, err := r.inner.Read(buffer)
	if n > 0 && r.hasher != nil {
		_, _ = r.hasher.Write(buffer[:n])
	}
	if err != nil && err != io.EOF {
		return n, classifyStreamError("read object", err, "BodyRead", r.redactor)
	}
	if err == io.EOF && r.hasher != nil && !r.checked {
		r.checked = true
		actual := hex.EncodeToString(r.hasher.Sum(nil))
		if actual != r.expected.Value {
			return n, storage.NewOperationError("read object", storage.ErrorChecksumMismatch, false, "DigestMismatch", 0)
		}
	}
	return n, err
}

func (r *providerReadCloser) Close() error {
	if err := r.inner.Close(); err != nil {
		return classifyStreamError("close object", err, "BodyClose", r.redactor)
	}
	return nil
}

func classifyStreamError(operation string, err error, providerCode string, redactor *secretRedactor) error {
	classified := classifyProviderError(operation, err, redactor)
	var operationErr *storage.OperationError
	if errors.As(classified, &operationErr) && operationErr != nil && operationErr.Code != storage.ErrorProvider {
		return classified
	}
	return storage.NewOperationError(operation, storage.ErrorUnavailable, true, providerCode, 0)
}

func checksumBase64(checksum storage.Checksum) string {
	decoded, _ := hex.DecodeString(checksum.Value)
	return base64.StdEncoding.EncodeToString(decoded)
}

func checksumFromProvider(providerValue *string, metadata map[string]string, checksumType types.ChecksumType) (storage.Checksum, error) {
	if value := strings.ToLower(strings.TrimSpace(metadata[checksumMetadataKey])); value != "" {
		checksum := storage.Checksum{Algorithm: storage.ChecksumSHA256, Value: value}
		if checksum.Validate() == nil {
			return checksum, nil
		}
		return storage.Checksum{}, storage.NewOperationError("read object checksum", storage.ErrorChecksumMismatch, false, "MalformedMetadataChecksum", 0)
	}
	if providerValue == nil {
		return storage.Checksum{}, nil
	}
	// A COMPOSITE checksum covers the ordered part checksums, not the full object
	// bytes. It is valid provider metadata but cannot satisfy Blob Plane's
	// full-body SHA-256 contract. Everstack metadata, when present, wins above.
	if checksumType == types.ChecksumTypeComposite {
		return storage.Checksum{}, nil
	}
	value := strings.TrimSpace(*providerValue)
	if value == "" {
		return storage.Checksum{}, nil
	}
	decoded, err := base64.StdEncoding.DecodeString(value)
	if err != nil || len(decoded) != sha256.Size {
		return storage.Checksum{}, storage.NewOperationError("read object checksum", storage.ErrorChecksumMismatch, false, "MalformedProviderChecksum", 0)
	}
	return storage.Checksum{Algorithm: storage.ChecksumSHA256, Value: hex.EncodeToString(decoded)}, nil
}

func validateMultipartOptions(options storage.MultipartOptions) error {
	if options.ExpectedSize != nil && *options.ExpectedSize < 0 {
		return storage.NewOperationError("begin multipart upload", storage.ErrorInvalidArgument, false, "", 0)
	}
	return options.Checksum.Validate()
}

func validateUpload(upload storage.MultipartUpload) error {
	if upload.Bucket == "" || upload.Key == "" || upload.ID == "" {
		return storage.NewOperationError("multipart upload", storage.ErrorInvalidArgument, false, "", 0)
	}
	return validateMultipartOptions(upload.Options)
}

func optionalString(value string) *string {
	if value == "" {
		return nil
	}
	return aws.String(value)
}

func trimETag(value *string) string {
	return strings.Trim(aws.ToString(value), "\"")
}

func classifyProviderError(operation string, err error, redactor *secretRedactor) error {
	if err == nil {
		return nil
	}
	var existing *storage.OperationError
	if errors.As(err, &existing) && existing != nil {
		return existing
	}
	if errors.Is(err, context.DeadlineExceeded) {
		return storage.NewOperationError(operation, storage.ErrorTimeout, true, "", 0)
	}
	if errors.Is(err, context.Canceled) {
		return storage.NewOperationError(operation, storage.ErrorUnavailable, false, "", 0)
	}

	providerCode := ""
	var apiError smithy.APIError
	if errors.As(err, &apiError) {
		providerCode = apiError.ErrorCode()
	}
	status := 0
	var responseError *smithyhttp.ResponseError
	if errors.As(err, &responseError) {
		status = responseError.HTTPStatusCode()
	}

	lowerCode := strings.ToLower(providerCode)
	code := storage.ErrorProvider
	retryable := false
	switch {
	case lowerCode == "nosuchkey" || lowerCode == "notfound" || lowerCode == "nosuchbucket" || status == 404:
		code = storage.ErrorNotFound
	case lowerCode == "preconditionfailed" || status == 412:
		code = storage.ErrorAlreadyExists
	case lowerCode == "conditionalrequestconflict" || status == 409:
		code = storage.ErrorConflict
		retryable = true
	case lowerCode == "baddigest" || lowerCode == "invaliddigest" || lowerCode == "checksummismatch":
		code = storage.ErrorChecksumMismatch
	case lowerCode == "requestexpired" || lowerCode == "expiredtoken":
		code = storage.ErrorExpired
	case lowerCode == "accessdenied" || lowerCode == "invalidaccesskeyid" || lowerCode == "signaturedoesnotmatch" || status == 401 || status == 403:
		code = storage.ErrorUnauthorized
	case lowerCode == "slowdown" || strings.Contains(lowerCode, "throttl") || status == 429 || status == 503:
		code = storage.ErrorThrottled
		retryable = true
	case lowerCode == "notimplemented" || lowerCode == "notsupported" || lowerCode == "methodnotallowed" || status == 405 || status == 501:
		code = storage.ErrorUnsupported
	case status >= 500:
		code = storage.ErrorUnavailable
		retryable = true
	default:
		var networkError net.Error
		if errors.As(err, &networkError) && networkError.Timeout() {
			code = storage.ErrorTimeout
			retryable = true
		}
	}
	if redactor != nil {
		providerCode = redactor.Redact(providerCode)
	}
	return storage.NewOperationError(operation, code, retryable, providerCode, status)
}

var _ storage.BlobPlane = (*Store)(nil)
