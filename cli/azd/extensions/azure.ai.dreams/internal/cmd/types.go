// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

import "time"

const (
	maxDreamChars       = 8000
	defaultContainer    = "dreams"
	defaultAIApiVersion = "2024-10-21"
)

type dreamRecord struct {
	ID             string               `json:"id"`
	Title          string               `json:"title"`
	Text           string               `json:"text"`
	CreatedAt      time.Time            `json:"createdAt"`
	Interpretation *dreamInterpretation `json:"interpretation,omitempty"`
}

type dreamSummary struct {
	ID        string    `json:"id"`
	Title     string    `json:"title"`
	CreatedAt time.Time `json:"createdAt"`
}

type dreamInterpretation struct {
	Summary   string   `json:"summary,omitempty"`
	Sentiment string   `json:"sentiment,omitempty"`
	Themes    []string `json:"themes,omitempty"`
	Symbols   []string `json:"symbols,omitempty"`
	Raw       string   `json:"raw"`
}

type interpretResult struct {
	DreamID          string              `json:"dreamId,omitempty"`
	Interpretation   dreamInterpretation `json:"interpretation"`
	InterpretationAt time.Time           `json:"interpretationAt"`
}
