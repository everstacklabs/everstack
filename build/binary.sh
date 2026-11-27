export VERSION=${VERSION:-v0.1.0}
export COMMIT_SHA=${COMMIT_SHA:-$(git rev-parse HEAD 2>/dev/null || echo unknown)}
export BUILD_DATE=${BUILD_DATE:-$(date -u '+%Y-%m-%dT%H:%M:%SZ')}
mkdir -p dist

# linux
for ARCH in amd64 arm64; do \
  GOOS=linux GOARCH=$ARCH CGO_ENABLED=0 \
  go build -trimpath -ldflags "-s -w -X github.com/everstacklabs/everstack/cmd/build.version=$VERSION -X github.com/everstacklabs/everstack/cmd/build.commit=$COMMIT_SHA -X github.com/everstacklabs/everstack/cmd/build.date=$BUILD_DATE" \
  -o dist/everstack-linux-$ARCH main.go; \
done

# macOS
for ARCH in amd64 arm64; do \
  GOOS=darwin GOARCH=$ARCH CGO_ENABLED=0 \
  go build -trimpath -ldflags "-s -w -X github.com/everstacklabs/everstack/cmd/build.version=$VERSION -X github.com/everstacklabs/everstack/cmd/build.commit=$COMMIT_SHA -X github.com/everstacklabs/everstack/cmd/build.date=$BUILD_DATE" \
  -o dist/everstack-darwin-$ARCH main.go; \
done

# windows
GOOS=windows GOARCH=amd64 CGO_ENABLED=0 \
go build -trimpath -ldflags "-s -w -X github.com/everstacklabs/everstack/cmd/build.version=$VERSION -X github.com/everstacklabs/everstack/cmd/build.commit=$COMMIT_SHA -X github.com/everstacklabs/everstack/cmd/build.date=$BUILD_DATE" \
-o dist/everstack-windows-amd64.exe main.go

# package + checksums
cd dist
for f in *; do tar -czf "$f.tgz" "$f"; done
shasum -a 256 * > SHA256SUMS.txt
