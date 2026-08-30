package serve

import (
	"context"
	"errors"
	"fmt"
	"net/url"
	"os"
	"regexp"
	"strings"

	storagepkg "github.com/everstacklabs/everstack/internal/storage"
	managedstorage "github.com/everstacklabs/everstack/internal/storage/managed"
	s3store "github.com/everstacklabs/everstack/internal/storage/s3"
	"github.com/jmoiron/sqlx"
)

const (
	managedStorageEnabledEnv     = "EVS_MANAGED_STORAGE_ENABLED"
	managedStorageCellIDEnv      = "EVS_MANAGED_STORAGE_CELL_ID"
	managedStorageR2EndpointEnv  = "EVS_MANAGED_STORAGE_R2_ENDPOINT"
	managedStorageR2RegionEnv    = "EVS_MANAGED_STORAGE_R2_REGION"
	managedStorageR2BucketEnv    = "EVS_MANAGED_STORAGE_R2_BUCKET"
	managedStorageR2AccessKeyEnv = "EVS_MANAGED_STORAGE_R2_ACCESS_KEY"
	managedStorageR2SecretKeyEnv = "EVS_MANAGED_STORAGE_R2_SECRET_KEY"
)

var (
	managedStorageCellIDPattern = regexp.MustCompile(`^[a-z0-9]([a-z0-9-]{0,61}[a-z0-9])?$`)
	managedStorageBucketPattern = regexp.MustCompile(`^[a-z0-9][a-z0-9-]{1,61}[a-z0-9]$`)
	managedStorageR2EUHost      = regexp.MustCompile(`^[a-f0-9]{32}\.eu\.r2\.cloudflarestorage\.com$`)
)

type managedStorageR2Config struct {
	CellID          string
	Endpoint        string
	Region          string
	Bucket          string
	AccessKeyID     string
	SecretAccessKey string
}

type managedStorageRuntime struct {
	cellID   string
	defaults storagepkg.ManagedDefaultEnsurer
	resolver storagepkg.ManagedStoreResolver
}

type managedStorageS3Factory func(context.Context, s3store.Config) (storagepkg.ObjectStore, error)

func defaultManagedStorageS3Factory(ctx context.Context, config s3store.Config) (storagepkg.ObjectStore, error) {
	return s3store.New(ctx, config)
}

func loadManagedStorageR2Config(getenv func(string) string) (managedStorageR2Config, bool, error) {
	if getenv == nil {
		getenv = os.Getenv
	}

	enabledValue := strings.ToLower(strings.TrimSpace(getenv(managedStorageEnabledEnv)))
	switch enabledValue {
	case "", "false":
		return managedStorageR2Config{}, false, nil
	case "true":
		// Continue below.
	default:
		return managedStorageR2Config{}, false, fmt.Errorf("%s must be true or false", managedStorageEnabledEnv)
	}

	values := map[string]string{
		managedStorageCellIDEnv:      strings.TrimSpace(getenv(managedStorageCellIDEnv)),
		managedStorageR2EndpointEnv:  strings.TrimSpace(getenv(managedStorageR2EndpointEnv)),
		managedStorageR2RegionEnv:    strings.TrimSpace(getenv(managedStorageR2RegionEnv)),
		managedStorageR2BucketEnv:    strings.TrimSpace(getenv(managedStorageR2BucketEnv)),
		managedStorageR2AccessKeyEnv: strings.TrimSpace(getenv(managedStorageR2AccessKeyEnv)),
		managedStorageR2SecretKeyEnv: strings.TrimSpace(getenv(managedStorageR2SecretKeyEnv)),
	}
	for _, name := range []string{
		managedStorageCellIDEnv,
		managedStorageR2EndpointEnv,
		managedStorageR2RegionEnv,
		managedStorageR2BucketEnv,
		managedStorageR2AccessKeyEnv,
		managedStorageR2SecretKeyEnv,
	} {
		if values[name] == "" {
			return managedStorageR2Config{}, true, fmt.Errorf("%s is required when %s=true", name, managedStorageEnabledEnv)
		}
	}

	if !managedStorageCellIDPattern.MatchString(values[managedStorageCellIDEnv]) {
		return managedStorageR2Config{}, true, fmt.Errorf("%s must be a lowercase cell slug", managedStorageCellIDEnv)
	}
	endpoint, err := normalizeManagedStorageR2EUEndpoint(values[managedStorageR2EndpointEnv])
	if err != nil {
		return managedStorageR2Config{}, true, fmt.Errorf("%s must identify an EU-jurisdiction R2 account endpoint", managedStorageR2EndpointEnv)
	}
	if !strings.EqualFold(values[managedStorageR2RegionEnv], "auto") {
		return managedStorageR2Config{}, true, fmt.Errorf("%s must be auto for R2 signing", managedStorageR2RegionEnv)
	}
	if !managedStorageBucketPattern.MatchString(values[managedStorageR2BucketEnv]) {
		return managedStorageR2Config{}, true, fmt.Errorf("%s must be a valid private R2 bucket name", managedStorageR2BucketEnv)
	}

	return managedStorageR2Config{
		CellID:          values[managedStorageCellIDEnv],
		Endpoint:        endpoint,
		Region:          "auto",
		Bucket:          values[managedStorageR2BucketEnv],
		AccessKeyID:     values[managedStorageR2AccessKeyEnv],
		SecretAccessKey: values[managedStorageR2SecretKeyEnv],
	}, true, nil
}

func normalizeManagedStorageR2EUEndpoint(raw string) (string, error) {
	parsed, err := url.Parse(raw)
	if err != nil || !strings.EqualFold(parsed.Scheme, "https") || parsed.Host == "" || parsed.Hostname() == "" {
		return "", errors.New("managed storage R2 endpoint requires HTTPS")
	}
	if parsed.User != nil || parsed.Port() != "" || parsed.RawQuery != "" || parsed.ForceQuery || parsed.Fragment != "" || parsed.Opaque != "" {
		return "", errors.New("managed storage R2 endpoint contains forbidden URL components")
	}
	if parsed.EscapedPath() != "" && parsed.EscapedPath() != "/" {
		return "", errors.New("managed storage R2 endpoint cannot contain a path")
	}
	host := strings.ToLower(parsed.Hostname())
	if !managedStorageR2EUHost.MatchString(host) {
		return "", errors.New("managed storage R2 endpoint is not EU jurisdictional")
	}
	return "https://" + host, nil
}

func buildManagedStorageRuntime(
	ctx context.Context,
	db *sqlx.DB,
	managedGateway bool,
	getenv func(string) string,
	newStore managedStorageS3Factory,
) (*managedStorageRuntime, error) {
	config, enabled, err := loadManagedStorageR2Config(getenv)
	if err != nil {
		return nil, fmt.Errorf("configure managed storage: %w", err)
	}
	if !enabled {
		return nil, nil
	}
	if !managedGateway {
		return nil, errors.New("configure managed storage: Everstack Storage can only be enabled on a managed gateway")
	}
	if db == nil {
		return nil, errors.New("configure managed storage: PostgreSQL is required")
	}
	if newStore == nil {
		newStore = defaultManagedStorageS3Factory
	}

	baseStore, err := newStore(ctx, s3store.Config{
		Endpoint:             config.Endpoint,
		Region:               config.Region,
		Bucket:               config.Bucket,
		AccessKeyID:          config.AccessKeyID,
		SecretAccessKey:      config.SecretAccessKey,
		ForcePathStyle:       true,
		EnforceManagedEgress: true,
		DisableNativeCopy:    true,
		WireChecksum:         s3store.WireChecksumContentMD5,
	})
	if err != nil {
		return nil, fmt.Errorf("configure managed storage cell client: %w", err)
	}
	if baseStore == nil {
		return nil, errors.New("configure managed storage cell client: store is nil")
	}

	resolver, err := managedstorage.NewResolver(managedstorage.Cell{
		ID:     config.CellID,
		Bucket: config.Bucket,
		Store:  baseStore,
	})
	if err != nil {
		return nil, fmt.Errorf("configure managed storage resolver: %w", err)
	}
	defaults, err := storagepkg.NewPostgresManagedDefaults(db, config.CellID)
	if err != nil {
		return nil, fmt.Errorf("configure managed storage defaults: %w", err)
	}

	return &managedStorageRuntime{
		cellID:   config.CellID,
		defaults: defaults,
		resolver: resolver,
	}, nil
}
