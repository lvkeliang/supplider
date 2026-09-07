//go:build enterprise

package objectfactory

import (
	"github.com/supplider/supplider/backend/internal/objectstore"
)

// newTierObjectStore wires enterprise-tier object storage.
//
// ROADMAP: S3-compatible storage (aws-sdk-go or MinIO client) behind the
// objectstore.Store port. Until that adapter lands, attachments are
// disabled (nil store → HTTP 501) rather than silently writing to local
// disk, which a multi-replica K8s deployment would not share.
func newTierObjectStore(_ Config) (objectstore.Store, error) {
	return nil, nil
}
