package main

import (
	"slices"

	"git-mirror/internal/authkeys"
	"git-mirror/internal/config"
)

// tokensFor lists the tokens that may access a mirror, in the order clone
// commands present them: "default" first, then unrestricted tokens, then
// scoped tokens that allow the mirror.
func tokensFor(tokens []config.Token, name string) []config.Token {
	var out []config.Token
	if t := findToken(tokens, config.DefaultTokenName); t != nil && t.Allows(name) {
		out = append(out, *t)
	}
	for _, scoped := range []bool{false, true} {
		for _, t := range tokens {
			if t.Name == config.DefaultTokenName || (len(t.Repos) > 0) != scoped || !t.Allows(name) {
				continue
			}
			out = append(out, t)
		}
	}
	return out
}

// pickToken chooses the token used in a single clone URL for a mirror (the
// first of tokensFor). nil when no token has access.
func pickToken(tokens []config.Token, name string) *config.Token {
	if ts := tokensFor(tokens, name); len(ts) > 0 {
		return &ts[0]
	}
	return nil
}

func findToken(tokens []config.Token, name string) *config.Token {
	i := slices.IndexFunc(tokens, func(t config.Token) bool { return t.Name == name })
	if i < 0 {
		return nil
	}
	return &tokens[i]
}

// repoAccess lists the tokens (by name) and client keys (by comment or
// fingerprint) that may access a mirror.
func repoAccess(tokens []config.Token, keys []authkeys.Key, name string) (tokenNames, keyLabels []string) {
	for _, t := range tokens {
		if t.Allows(name) {
			tokenNames = append(tokenNames, t.Name)
		}
	}
	for _, k := range keys {
		if k.Allows(name) {
			label := k.Comment
			if label == "" {
				label = k.Fingerprint()
			}
			keyLabels = append(keyLabels, label)
		}
	}
	return tokenNames, keyLabels
}
