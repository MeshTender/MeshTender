package observe

import (
	"bytes"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
)

// Packet is the trusted part of a packets-topic message: the raw LoRa packet.
// Uplinks also report the observer radio's SNR and RSSI; MeshTender doesn't
// keep signal readings, so they aren't decoded.
type Packet struct {
	Raw []byte
}

// ParsePacket decodes a packets-topic payload.
func ParsePacket(payload []byte) (Packet, error) {
	var env struct {
		Raw string `json:"raw"`
	}
	if err := json.Unmarshal(payload, &env); err != nil {
		return Packet{}, fmt.Errorf("observe: packet envelope: %w", err)
	}
	if env.Raw == "" {
		return Packet{}, errors.New("observe: packet envelope has no raw")
	}
	raw, err := hex.DecodeString(env.Raw)
	if err != nil {
		return Packet{}, fmt.Errorf("observe: raw: %w", err)
	}
	return Packet{Raw: raw}, nil
}

// Status is an observer's self-reported health from its status topic. Every
// field is optional: each uplink program reports a different subset.
type Status struct {
	Online          bool
	ClientVersion   string
	FirmwareVersion string
	Model           string
	BatteryMV       *int
	NoiseFloor      *int
	UptimeSecs      *int
}

// ParseStatus decodes a status-topic payload.
func ParseStatus(payload []byte) (Status, error) {
	var env struct {
		Status          string `json:"status"`
		ClientVersion   string `json:"client_version"`
		FirmwareVersion string `json:"firmware_version"`
		Model           string `json:"model"`
		Stats           struct {
			BatteryMV  flexNumber `json:"battery_mv"`
			NoiseFloor flexNumber `json:"noise_floor"`
			UptimeSecs flexNumber `json:"uptime_secs"`
		} `json:"stats"`
	}
	if err := json.Unmarshal(payload, &env); err != nil {
		return Status{}, fmt.Errorf("observe: status envelope: %w", err)
	}
	return Status{
		Online:          env.Status == "online",
		ClientVersion:   env.ClientVersion,
		FirmwareVersion: env.FirmwareVersion,
		Model:           env.Model,
		BatteryMV:       env.Stats.BatteryMV.int(),
		NoiseFloor:      env.Stats.NoiseFloor.int(),
		UptimeSecs:      env.Stats.UptimeSecs.int(),
	}, nil
}

// flexNumber accepts a JSON number or a numeric string (uplinks differ), and
// remembers absence. An unparseable value reads as absent rather than failing
// the whole message: signal stats are nice-to-have.
type flexNumber struct {
	v  float64
	ok bool
}

func (n *flexNumber) UnmarshalJSON(b []byte) error {
	b = bytes.Trim(b, `"`)
	if len(b) == 0 || string(b) == "null" {
		return nil
	}
	if v, err := strconv.ParseFloat(string(b), 64); err == nil {
		n.v, n.ok = v, true
	}
	return nil
}

func (n flexNumber) int() *int {
	if !n.ok {
		return nil
	}
	v := int(n.v)
	return &v
}
