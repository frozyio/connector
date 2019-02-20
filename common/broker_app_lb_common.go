package common

import (
	"net"
	"time"

	"github.com/satori/go.uuid"
)

// BrokerInfoData is shared struct between AD/ALB service and Connector to carry on
// information about brokers
type BrokerInfoData struct {
	BrokerPublicKey []byte    `json:"broker_public_key"`
	BrokerTTL       time.Time `json:"broker_ttl"`
	BrokerID        uuid.UUID `json:"broker_id"`
	BrokerName      string    `json:"broker_name"`
	BrokerIP        net.IP    `json:"broker_ip"`
	BrokerPort      uint16    `json:"broker_port"`
	BrokerGeoScore  uint      `json:"broker_geo_score"`
	BrokerLoadScore uint      `json:"broker_load_score"`
}
