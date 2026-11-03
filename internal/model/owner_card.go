// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 The tmx authors

package model

// CardEffect describes what a human action touches. It is computed by the server,
// not an authorization or a promise that an action is safe.
type CardEffect string

const (
	CardEffectNone    CardEffect = "none"
	CardEffectMoney   CardEffect = "money"
	CardEffectOutward CardEffect = "outward"
	CardEffectAccount CardEffect = "account"
)

type CardAction struct {
	Goal       string     `json:"goal"`
	Steps      []string   `json:"steps"`
	Account    string     `json:"account,omitempty"`
	Success    string     `json:"success"`
	DoNotTouch []string   `json:"do_not_touch,omitempty"`
	Effects    CardEffect `json:"effects"`
}

// OwnerCard is the typed content of an ask_owner card. Version changes when
// content or deferral state changes, not when it is answered.
type OwnerCard struct {
	Type           string      `json:"type"` // decision | human_action
	Context        string      `json:"context,omitempty"`
	Recommendation string      `json:"recommendation,omitempty"`
	Category       string      `json:"category,omitempty"`
	Action         *CardAction `json:"action,omitempty"`
}
