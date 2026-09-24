# OurWay RMM Release Process

This document describes the process for creating and publishing an OurWay RMM
release. Follow these steps for every release, including patches.

## Prerequisites

- Go 1.26+ toolchain
- Docker and Docker Compose
- Write access to the GitHub repository
- Write access to GitHub Container Registry (`ghcr.io`)

Signing needs no extra tools: `make sign` builds the minisign signer from
`tools/signer` (plain `go build`), and CI signs the server image with keyless
cosign (Sigstore, GitHub OIDC).

## Release Checklist

### 1. Prepare the Release

```sh
# Update the version in every place that carries it:
#   server/cmd/server/main.go       — default in env("OURWAY_RMM_VERSION", "1.2.0")
#   Makefile                        — VERSION := 1.2.0
#   .env.prod.example               — OURWAY_RMM_VERSION
#   docker-compose.release.yml      — image tags
#   docker-compose.release-byop.yml — image tags

# Write release notes
# Edit docs/releases/vX.Y.Z.md with feature summary

# Update CHANGELOG.md
# Document all changes since the previous release
```

### 2. Build Everything

```sh
# Full build
make build

# All tests
make test

# Agent verification
make verify-agent

# Frontend build
cd frontend && npm run build && cd ..
```

### 3. Cross-Compile Agent Binaries

```sh
VERSION=X.Y.Z make agent
```

This produces static binaries for:

- Linux amd64, arm64
- Windows amd64, arm64
- macOS amd64, arm64

### 4. Sign Artifacts

```sh
# Sign agent binaries with minisign
MINISIGN_PASS=password VERSION=X.Y.Z make sign

# Verify signatures
make verify-sigs

# Build + push the server image (the image is named ourway-rmm-server)
docker build -t ourway-rmm-server:${VERSION} -f server/Dockerfile .
docker push ghcr.io/welcometotheweb/ourway-rmm-server:${VERSION}

# Sign the image with keyless cosign (Sigstore). In the tag-push flow CI does
# this automatically (GitHub OIDC, id-token: write); equivalent command,
# signing by digest:
cosign sign --yes "ghcr.io/welcometotheweb/ourway-rmm-server@${IMAGE_DIGEST}"
```

### 5. Generate SBOMs

```sh
# Generate CycloneDX SBOMs for all agent binaries
VERSION=X.Y.Z make sbom
```

### 6. Tag and Push

```sh
git tag -a v${VERSION} -m "Release ${VERSION}"
git push origin main
git push origin v${VERSION}
```

### 7. Publish Release on GitHub

Create a GitHub release with:

- All signed agent binaries
- Checksums file
- SBOM files
- Docker image tags
- Release notes

## Release Artifacts

Every release must include:

| Artifact | Description |
| -------- | ----------- |
| Agent binaries (6 platforms) | Static Linux/Windows/macOS executables |
| Agent minisig signatures | Per-binary cryptographic signatures |
| Checksums file | SHA-256 hashes for all binaries |
| Docker image | Single-arch (amd64) image for the server |
| SBOM files | CycloneDX format for each binary |
| Release notes | Human-readable change summary |

## Versioning

OurWay RMM follows semantic versioning:

- **Major** (1.0.0): Breaking API changes
- **Minor** (1.1.0): New features, backward-compatible
- **Patch** (1.0.1): Bug fixes, backward-compatible

## Post-Release

- Verify deployment of signed artifacts
- Announce the release (GitHub, mailing list, etc.)
- Update documentation links to the new version

