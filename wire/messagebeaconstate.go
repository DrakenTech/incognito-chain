package wire

import (
	"encoding/json"

	"time"

	"github.com/libp2p/go-libp2p-peer"
	"github.com/ninjadotorg/constant/cashec"
)

const (
	MaxBeaconStatePayload = 4000000 // 4 Mb
)

type MessageBeaconState struct {
	Timestamp time.Time
	ChainInfo interface{}
	SenderID  string
}

func (self *MessageBeaconState) MessageType() string {
	return CmdBeaconState
}

func (self *MessageBeaconState) MaxPayloadLength(pver int) int {
	return MaxBeaconStatePayload
}

func (self *MessageBeaconState) JsonSerialize() ([]byte, error) {
	jsonBytes, err := json.Marshal(self)
	return jsonBytes, err
}

func (self *MessageBeaconState) JsonDeserialize(jsonStr string) error {
	err := json.Unmarshal([]byte(jsonStr), self)
	return err
}

func (self *MessageBeaconState) SetSenderID(senderID peer.ID) error {
	self.SenderID = senderID.Pretty()
	return nil
}

func (self *MessageBeaconState) SignMsg(_ *cashec.KeySet) error {
	return nil
}

func (self *MessageBeaconState) VerifyMsgSanity() error {
	return nil
}
