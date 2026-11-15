package management

import (
	"context"
	"crypto/sha256"
	"fmt"
	"net/http"
	"strings"
	"time"

	"ferry/internal/codingplan"
	"ferry/internal/domain"
	"ferry/internal/nativeauth"

	"github.com/google/uuid"
)

type codingUsageAuth struct {
	secret, nativeRef string
}

type codingUsageEntry struct {
	id     string
	usage  *codingplan.Snapshot
	flight *codingUsageFlight
}

type codingUsageFlight struct {
	done  chan struct{}
	usage *codingplan.Snapshot
	err   error
}

// Bind the pool to the official quota endpoint and effective credential, not
// the inference protocol or secret reference. Two references to the same key
// share this display-only pool; product/region endpoints remain independent.
func (s *Server) codingUsageBinding(providerID, connectionID string) (codingplan.Source, codingUsageAuth, [32]byte, int, string) {
	provider, connection := s.v2ProviderConnection(providerID, connectionID)
	if provider == nil || connection == nil {
		return codingplan.Source{}, codingUsageAuth{}, [32]byte{}, http.StatusNotFound, "provider connection not found"
	}
	baseURL := connectionBaseURL(provider, connection)
	if connection.FullURL != "" {
		baseURL = connection.FullURL
	}
	source, err := codingplan.SourceForURL(baseURL)
	if err != nil || (provider.Type != "" && provider.Type != domain.ProviderCustom) {
		return codingplan.Source{}, codingUsageAuth{}, [32]byte{}, http.StatusUnprocessableEntity, "connection does not expose supported Coding Plan usage"
	}
	ref := connection.Credential.SecretRef
	_, oauthRef := domain.OAuthAccountFromRef(ref)
	driver, _, nativeRef := nativeauth.ParseRef(ref)
	if nativeRef && (driver == "kimi" || driver == "kimi-ai") && source.Provider == "kimi-coding" {
		if s.deps.NativeOAuth == nil {
			return codingplan.Source{}, codingUsageAuth{}, [32]byte{}, http.StatusServiceUnavailable, "native authentication unavailable"
		}
		if connection.FullURL != "" || s.deps.NativeOAuth.ValidateBinding(ref, baseURL, string(domain.ProtocolOpenAIChat)) != nil {
			return codingplan.Source{}, codingUsageAuth{}, [32]byte{}, http.StatusUnprocessableEntity, "native credential requires its registered provider endpoint"
		}
		binding, err := s.deps.NativeOAuth.RuntimeIdentity(ref)
		if err != nil {
			return codingplan.Source{}, codingUsageAuth{}, [32]byte{}, http.StatusUnprocessableEntity, "native credential unavailable"
		}
		identity := sha256.Sum256([]byte(fmt.Sprintf("native\x00%s\x00%s\x00%s", source.URL, ref, binding)))
		return source, codingUsageAuth{nativeRef: ref}, identity, 0, ""
	}
	if oauthRef || nativeRef {
		return codingplan.Source{}, codingUsageAuth{}, [32]byte{}, http.StatusUnprocessableEntity, "connection credential does not support this Coding Plan usage endpoint"
	}
	if s.deps.Resolver == nil {
		return codingplan.Source{}, codingUsageAuth{}, [32]byte{}, http.StatusServiceUnavailable, "credential resolver not configured"
	}
	secret, err := s.deps.Resolver.Resolve(ref)
	if err != nil || strings.TrimSpace(secret) == "" {
		return codingplan.Source{}, codingUsageAuth{}, [32]byte{}, http.StatusUnprocessableEntity, "credential unavailable"
	}
	identity := sha256.Sum256([]byte(fmt.Sprintf("key\x00%s\x00%s", source.URL, secret)))
	return source, codingUsageAuth{secret: secret}, identity, 0, ""
}

// This endpoint keeps the existing Codex API compatible while offering the
// same cache/refresh semantics to subscription API-key connections. Coding
// snapshots are display-only and do not alter scheduler quota or billing.
func (s *Server) handleV2ConnectionUsage(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	providerID, connectionID := r.PathValue("id"), r.PathValue("connection_id")
	provider, connection := s.v2ProviderConnection(providerID, connectionID)
	if provider == nil || connection == nil {
		writeMgmtErr(w, http.StatusNotFound, "provider connection not found")
		return
	}
	if provider.Type == domain.ProviderCliproxyCodex {
		s.handleV2ConnectionOAuthUsage(w, r)
		return
	}
	source, auth, identity, status, message := s.codingUsageBinding(providerID, connectionID)
	if status != 0 {
		writeMgmtErr(w, status, message)
		return
	}
	if expected := r.URL.Query().Get("quota_group_id"); expected != "" && expected != s.codingQuotaGroupID(identity) {
		writeMgmtErr(w, http.StatusConflict, "quota membership changed; refresh provider accounts")
		return
	}
	usage, err := s.readCodingUsage(r.Context(), identity, source, auth, r.URL.Query().Get("refresh") != "false")
	if err != nil {
		writeMgmtErr(w, http.StatusBadGateway, "read Coding Plan usage: "+err.Error())
		return
	}
	// A request started for a replaced/deleted account must not return its
	// snapshot under the new connection identity.
	_, _, currentIdentity, status, _ := s.codingUsageBinding(providerID, connectionID)
	if status != 0 || currentIdentity != identity {
		writeMgmtErr(w, http.StatusConflict, "connection credential changed while reading usage; refresh again")
		return
	}
	writeMgmtJSON(w, http.StatusOK, map[string]any{
		"provider_id": providerID, "connection_id": connectionID, "usage": usage, "quota_group_id": s.codingQuotaGroupID(identity),
	})
}

// Opaque IDs are process-local random handles, never credential digests. The
// frontend must obtain membership from a fresh provider view after edits.
func (s *Server) codingQuotaEntryLocked(identity [32]byte) *codingUsageEntry {
	if s.codingUsage == nil {
		s.codingUsage = make(map[[32]byte]*codingUsageEntry)
	}
	entry := s.codingUsage[identity]
	if entry == nil {
		entry = &codingUsageEntry{id: "quota-" + uuid.NewString()}
		s.codingUsage[identity] = entry
	}
	return entry
}

func (s *Server) codingQuotaGroupID(identity [32]byte) string {
	s.codingUsageMu.Lock()
	defer s.codingUsageMu.Unlock()
	return s.codingQuotaEntryLocked(identity).id
}

func (s *Server) codingQuotaMembership(providerID, connectionID string) (string, string) {
	provider, connection := s.v2ProviderConnection(providerID, connectionID)
	if provider != nil && connection != nil && provider.Type == domain.ProviderCliproxyCodex && s.deps.OAuth != nil {
		account, ok := domain.OAuthAccountFromRef(connection.Credential.SecretRef)
		if !ok {
			return "", ""
		}
		view, ok := s.deps.OAuth.Account(account)
		if !ok || view.AccountID == "" {
			return "", ""
		}
		identity := sha256.Sum256([]byte("codex-quota\x00" + view.AccountID))
		return s.codingQuotaGroupID(identity), "codex"
	}

	source, _, identity, status, _ := s.codingUsageBinding(providerID, connectionID)
	if status != 0 {
		return "", ""
	}
	return s.codingQuotaGroupID(identity), source.Provider
}

func (s *Server) readCodingUsage(ctx context.Context, identity [32]byte, source codingplan.Source, auth codingUsageAuth, refresh bool) (*codingplan.Snapshot, error) {
	s.codingUsageMu.Lock()
	entry := s.codingQuotaEntryLocked(identity)
	if !refresh {
		usage := entry.usage
		s.codingUsageMu.Unlock()
		return usage, nil
	}
	if flight := entry.flight; flight != nil {
		s.codingUsageMu.Unlock()
		select {
		case <-ctx.Done():
			return nil, fmt.Errorf("quota request canceled")
		case <-flight.done:
			return flight.usage, flight.err
		}
	}
	flight := &codingUsageFlight{done: make(chan struct{})}
	entry.flight = flight
	s.codingUsageMu.Unlock()

	now := time.Now
	if s.deps.Now != nil {
		now = s.deps.Now
	}
	client := s.deps.SubscriptionUsageHTTPClient
	if auth.nativeRef != "" {
		client = s.deps.NativeOAuth.ClientFor(auth.nativeRef, client)
	}
	usage, err := codingplan.Fetch(ctx, client, source, auth.secret, now())
	s.codingUsageMu.Lock()
	if err == nil {
		entry.usage = usage
	}
	entry.flight = nil
	flight.usage, flight.err = usage, err
	close(flight.done)
	s.codingUsageMu.Unlock()
	return usage, err
}
