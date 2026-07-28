package lsp

import "context"

// Session caches one Client per language for the life of a single rgit
// invocation, so the cost of dialling is flat in the number of anchors
// rather than linear. It matters for the stdio servers: vtsls and pyright
// have no daemon, so every dial is a subprocess spawn and handshake.
//
// A language that degrades is remembered as degraded rather than redialled
// -- a server absent for the first anchor has not appeared by the second.
//
// Not safe for concurrent use; one invocation resolves in sequence.
type Session struct {
	clients  map[string]*Client
	degraded map[string]bool
}

func NewSession() *Session {
	return &Session{clients: map[string]*Client{}, degraded: map[string]bool{}}
}

// Dial returns a cached client for lang, dialling one on first use. It
// mirrors the package-level Dial's contract: degraded=true with a nil
// client means no live server, which callers treat as normal.
func (s *Session) Dial(ctx context.Context, lang, repoRoot string) (*Client, bool) {
	if s == nil {
		return Dial(ctx, lang, repoRoot)
	}
	if s.degraded[lang] {
		return nil, true
	}
	if c, ok := s.clients[lang]; ok {
		return c, false
	}

	client, deg := Dial(ctx, lang, repoRoot)
	if deg {
		s.degraded[lang] = true
		return nil, true
	}
	s.clients[lang] = client
	return client, false
}

// Close shuts every cached client down. Callers own the session for one
// invocation and must call this; the stdio servers are killed on close
// rather than left running.
func (s *Session) Close() {
	if s == nil {
		return
	}
	for lang, c := range s.clients {
		_ = c.Close()
		delete(s.clients, lang)
	}
}
