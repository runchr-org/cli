package external

import (
	"encoding/json"
	"errors"
	"fmt"
)

// jsonTranscript is a generic JSON transcript with a messages array.
// Used for built-in JSON chunking when transcript_format is "json".
type jsonTranscript struct {
	Messages []json.RawMessage `json:"messages"`
}

// chunkJSON splits a JSON transcript with a "messages" array at message boundaries.
func chunkJSON(content []byte, maxSize int) ([][]byte, error) {
	var transcript jsonTranscript
	if err := json.Unmarshal(content, &transcript); err != nil {
		return nil, fmt.Errorf("invalid JSON transcript: %w", err)
	}

	if len(transcript.Messages) == 0 {
		return [][]byte{content}, nil
	}

	var chunks [][]byte
	var currentMessages []json.RawMessage
	currentSize := len(`{"messages":[]}`) // Base JSON structure size

	for _, msg := range transcript.Messages {
		msgSize := len(msg) + 1 // +1 for comma separator

		if currentSize+msgSize > maxSize && len(currentMessages) > 0 {
			chunkData, err := json.Marshal(jsonTranscript{Messages: currentMessages})
			if err != nil {
				return nil, fmt.Errorf("failed to marshal chunk: %w", err)
			}
			chunks = append(chunks, chunkData)

			currentMessages = nil
			currentSize = len(`{"messages":[]}`)
		}

		currentMessages = append(currentMessages, msg)
		currentSize += msgSize
	}

	if len(currentMessages) > 0 {
		chunkData, err := json.Marshal(jsonTranscript{Messages: currentMessages})
		if err != nil {
			return nil, fmt.Errorf("failed to marshal final chunk: %w", err)
		}
		chunks = append(chunks, chunkData)
	}

	if len(chunks) == 0 {
		return nil, errors.New("failed to create any chunks")
	}

	return chunks, nil
}

// reassembleJSON merges JSON transcript chunks by combining their message arrays.
func reassembleJSON(chunks [][]byte) ([]byte, error) {
	var allMessages []json.RawMessage

	for _, chunk := range chunks {
		var transcript jsonTranscript
		if err := json.Unmarshal(chunk, &transcript); err != nil {
			return nil, fmt.Errorf("failed to unmarshal chunk: %w", err)
		}
		allMessages = append(allMessages, transcript.Messages...)
	}

	result, err := json.Marshal(jsonTranscript{Messages: allMessages})
	if err != nil {
		return nil, fmt.Errorf("failed to marshal reassembled transcript: %w", err)
	}
	return result, nil
}
