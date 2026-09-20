package executor

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"path"
)

type InputFile struct {
	ReferenceID string
	Filename    string
	Content     []byte
	SHA256      string
}

type Artifact struct {
	ObjectID  string
	Filename  string
	SizeBytes int64
	SHA256    string
}

type ExportRequest struct {
	Ownership
	LocalSessionID string
	SandboxID      string
	Filename       string
	Content        []byte
	SHA256         string
}

type InputRequest struct {
	Ownership
	LocalSessionID string
	SandboxID      string
	ReferenceID    string
}

// ArtifactBridge is implemented by the existing verified upload/download
// lifecycle. It intentionally contains no provider client or quota shortcut.
type ArtifactBridge interface {
	ResolveInput(context.Context, InputRequest) (*InputFile, error)
	Export(context.Context, ExportRequest) (*Artifact, error)
}

func (m *Manager) AttachInput(ctx context.Context, owner Ownership, localSessionID, referenceID, destination string, bridge ArtifactBridge) (string, error) {
	b, err := m.ownedBinding(ctx, owner, localSessionID)
	if err != nil {
		return "", err
	}
	return m.attachInput(ctx, b, referenceID, destination, bridge)
}

func (m *Manager) AttachInputReference(ctx context.Context, owner Ownership, localSessionID, sandboxID, referenceID, destination string, bridge ArtifactBridge) (string, error) {
	b, err := m.referenceBinding(ctx, owner, localSessionID, sandboxID)
	if err != nil {
		return "", err
	}
	return m.attachInput(ctx, b, referenceID, destination, bridge)
}

func (m *Manager) ReadWorkspaceFile(ctx context.Context, owner Ownership, localSessionID, name string) ([]byte, error) {
	b, err := m.ownedBinding(ctx, owner, localSessionID)
	if err != nil {
		return nil, err
	}
	resolved, err := ResolveWorkspacePath(name)
	if err != nil {
		return nil, err
	}
	return m.backend.ReadFile(ctx, b.owner, b.environment.SandboxID, resolved)
}

func (m *Manager) WriteWorkspaceFile(ctx context.Context, owner Ownership, localSessionID, name string, data []byte) error {
	b, err := m.ownedBinding(ctx, owner, localSessionID)
	if err != nil {
		return err
	}
	resolved, err := ResolveWorkspacePath(name)
	if err != nil {
		return err
	}
	return m.backend.WriteFile(ctx, b.owner, b.environment.SandboxID, resolved, data)
}

func (m *Manager) ListWorkspaceFiles(ctx context.Context, owner Ownership, localSessionID, name string) ([]string, error) {
	b, err := m.ownedBinding(ctx, owner, localSessionID)
	if err != nil {
		return nil, err
	}
	resolved, err := ResolveWorkspacePath(name)
	if err != nil {
		return nil, err
	}
	files, err := m.backend.ListFiles(ctx, b.owner, b.environment.SandboxID, resolved)
	if err != nil {
		return nil, err
	}
	names := make([]string, 0, len(files))
	for _, file := range files {
		names = append(names, file.Name)
	}
	return names, nil
}

func (m *Manager) ExportWorkspaceFile(ctx context.Context, owner Ownership, localSessionID, name string, bridge ArtifactBridge) (*Artifact, error) {
	b, err := m.ownedBinding(ctx, owner, localSessionID)
	if err != nil {
		return nil, err
	}
	return m.exportWorkspaceFile(ctx, b, name, bridge)
}

func (m *Manager) ExportWorkspaceFileReference(ctx context.Context, owner Ownership, localSessionID, sandboxID, name string, bridge ArtifactBridge) (*Artifact, error) {
	b, err := m.referenceBinding(ctx, owner, localSessionID, sandboxID)
	if err != nil {
		return nil, err
	}
	return m.exportWorkspaceFile(ctx, b, name, bridge)
}

func (m *Manager) referenceBinding(ctx context.Context, owner Ownership, localSessionID, sandboxID string) (*binding, error) {
	owner = normalizeOwner(owner)
	if localSessionID == "" || sandboxID == "" {
		return nil, errors.New("persisted executor reference is incomplete")
	}
	if err := m.backend.VerifyOwnership(ctx, owner, sandboxID); err != nil {
		return nil, fmt.Errorf("verify workspace ownership: %w", err)
	}
	return &binding{owner: owner, environment: Environment{LocalSessionID: localSessionID, SandboxID: sandboxID}}, nil
}

func (m *Manager) attachInput(ctx context.Context, b *binding, referenceID, destination string, bridge ArtifactBridge) (string, error) {
	if bridge == nil {
		return "", errors.New("verified artifact bridge is not configured")
	}
	input, err := bridge.ResolveInput(ctx, InputRequest{Ownership: b.owner, LocalSessionID: b.environment.LocalSessionID, SandboxID: b.environment.SandboxID, ReferenceID: referenceID})
	if err != nil {
		return "", err
	}
	if input == nil || input.ReferenceID != referenceID {
		return "", errors.New("artifact bridge returned an invalid input")
	}
	if destination == "" {
		destination = path.Join("inputs", path.Base(input.Filename))
	}
	resolved, err := ResolveWorkspacePath(destination)
	if err != nil {
		return "", err
	}
	if input.SHA256 != "" && checksum(input.Content) != input.SHA256 {
		return "", errors.New("input checksum mismatch")
	}
	if err = m.backend.WriteFile(ctx, b.owner, b.environment.SandboxID, resolved, input.Content); err != nil {
		return "", err
	}
	return resolved, nil
}

func (m *Manager) exportWorkspaceFile(ctx context.Context, b *binding, name string, bridge ArtifactBridge) (*Artifact, error) {
	if bridge == nil {
		return nil, errors.New("verified artifact bridge is not configured")
	}
	resolved, err := ResolveWorkspacePath(name)
	if err != nil {
		return nil, err
	}
	data, err := m.backend.ReadFile(ctx, b.owner, b.environment.SandboxID, resolved)
	if err != nil {
		return nil, err
	}
	return bridge.Export(ctx, ExportRequest{Ownership: b.owner, LocalSessionID: b.environment.LocalSessionID, SandboxID: b.environment.SandboxID, Filename: path.Base(resolved), Content: data, SHA256: checksum(data)})
}

func (m *Manager) ownedBinding(ctx context.Context, owner Ownership, localSessionID string) (*binding, error) {
	owner = normalizeOwner(owner)
	m.mu.RLock()
	b := m.bindings[localSessionID]
	m.mu.RUnlock()
	if b == nil || b.owner != owner {
		return nil, errors.New("environment is not owned by authenticated instance")
	}
	if err := m.backend.VerifyOwnership(ctx, owner, b.environment.SandboxID); err != nil {
		return nil, fmt.Errorf("verify workspace ownership: %w", err)
	}
	return b, nil
}

func checksum(data []byte) string { sum := sha256.Sum256(data); return hex.EncodeToString(sum[:]) }
