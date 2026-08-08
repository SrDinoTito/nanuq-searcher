package network

// TLS helpers for the outbound HTTP layer (TASK-007, CON-003).
//
// The cipher list is shuffled to keep the client's fingerprint off the
// most common server heuristics: the first three suites are kept in place
// (they carry the negotiation) and the remaining ones are permuted per
// process — the "ZenRows trick", port of network.py shuffle_ciphers
// (example/searxng/searx/network/client.py).

import (
	"crypto/tls"
	"math/rand"
)

// ShuffleCiphers permutes suites in place, keeping the first three entries
// fixed and shuffling the rest (port of shuffle_ciphers, client.py). It is
// a no-op for slices with three or fewer elements. The permutation is
// random per process — intentionally NOT crypto-random, the goal is only
// to break static fingerprinting, not security.
func ShuffleCiphers(suites []uint16) {
	if len(suites) <= 3 {
		return
	}
	tail := suites[3:]
	rand.Shuffle(len(tail), func(i, j int) {
		tail[i], tail[j] = tail[j], tail[i]
	})
}

// BuildTLSConfig builds a *tls.Config for the outbound transport with the
// secure cipher suites shuffled via ShuffleCiphers. verify controls TLS
// certificate verification (outgoing.verify, CON-003); a false value sets
// InsecureSkipVerify.
//
// It is deliberately NOT browser-like: no JA3-mimicking ciphers or
// extensions (documented deviation from the Python reference, which uses
// ssl.create_default_context). We only shuffle the suites Go already
// considers secure. Note the TLS 1.3 cipher suites are not configurable in
// Go and are ignored here; the shuffle applies to TLS 1.2 and below.
func BuildTLSConfig(verify bool) *tls.Config {
	secure := tls.CipherSuites()
	suites := make([]uint16, len(secure))
	for i, cs := range secure {
		suites[i] = cs.ID
	}
	ShuffleCiphers(suites)
	return &tls.Config{
		MinVersion:         tls.VersionTLS12,
		InsecureSkipVerify: !verify,
		CipherSuites:       suites,
	}
}
