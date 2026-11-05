package analyzer

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
)

// Record only message identifiers, never the report body or webhook URL. With
// wait=true the Discord response confirms the message was created.
type discordReceiptTransport struct{ base http.RoundTripper }

func (t discordReceiptTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	response, err := t.base.RoundTrip(req)
	if err != nil || response.StatusCode != http.StatusOK {
		return response, err
	}
	raw, readErr := io.ReadAll(io.LimitReader(response.Body, (256<<10)+1))
	response.Body.Close()
	if readErr != nil || len(raw) > 256<<10 {
		return nil, fmt.Errorf("Discord receipt could not be read")
	}
	var receipt struct {
		ID      string `json:"id"`
		Channel string `json:"channel_id"`
	}
	if json.Unmarshal(raw, &receipt) != nil || !snowflake(receipt.ID) || !snowflake(receipt.Channel) {
		return nil, fmt.Errorf("Discord did not return a valid message receipt")
	}
	log.Printf("analyzer discord_message_id=%s channel_id=%s", receipt.ID, receipt.Channel)
	response.Body = io.NopCloser(bytes.NewReader(raw))
	return response, nil
}
func snowflake(s string) bool {
	if len(s) < 1 || len(s) > 32 {
		return false
	}
	for _, c := range s {
		if c < '0' || c > '9' {
			return false
		}
	}
	return true
}
