// Package codeword generates and validates the short, human-spoken pairing
// codes used to introduce a sender and receiver to each other.
package codeword

import (
	"crypto/rand"
	"errors"
	"fmt"
	"math/big"
	"strings"
)

// WordCount is the number of words in the underlying list.
var WordCount = len(wordlist)

// NumWords is how many distinct words make up one generated code.
const NumWords = 3

// Generate returns a fresh code such as "crimson-otter-lagoon", drawn
// without replacement using a cryptographically secure random source
// (crypto/rand via math/big.Int, which does its own rejection sampling
// internally to avoid modulo bias).
func Generate() (string, error) {
	if WordCount < NumWords {
		return "", fmt.Errorf("codeword: wordlist too small (%d words)", WordCount)
	}

	chosen := make([]string, 0, NumWords)
	used := make(map[int]bool, NumWords)

	for len(chosen) < NumWords {
		n, err := rand.Int(rand.Reader, big.NewInt(int64(WordCount)))
		if err != nil {
			return "", fmt.Errorf("codeword: reading random index: %w", err)
		}
		idx := int(n.Int64())
		if used[idx] {
			continue
		}
		used[idx] = true
		chosen = append(chosen, wordlist[idx])
	}

	return strings.Join(chosen, "-"), nil
}

// ErrInvalidCode is returned by Validate for a code that could not possibly
// have come out of Generate — catching obvious typos before spending time
// on network discovery.
var ErrInvalidCode = errors.New("codeword: not a well-formed pairing code")

// Validate performs a cheap, local sanity check (right shape, words drawn
// from the known list) before the caller attempts network discovery. It
// does not and cannot confirm the code actually matches an active sender —
// only the discovery handshake can do that.
func Validate(code string) error {
	parts := strings.Split(code, "-")
	if len(parts) != NumWords {
		return ErrInvalidCode
	}
	known := make(map[string]bool, WordCount)
	for _, w := range wordlist {
		known[w] = true
	}
	for _, p := range parts {
		if !known[p] {
			return ErrInvalidCode
		}
	}
	return nil
}
