package sqlgo

import (
	"github.com/tyowk/sqlgo/internal/connector"
	"github.com/tyowk/sqlgo/internal/storage"
	"time"
)

func NewServer(addr string, pager *storage.Pager, schema *storage.SchemaManager, cred *connector.Credential) *connector.Server {
	return connector.NewServer(addr, pager, schema, cred)
}

func Open(target string, opts ...connector.Option) (connector.DB, error) {
	return connector.Open(target, opts...)
}

func WithPassword(p string) connector.Option { return func(o *connector.Options) { o.Password = p } }
func WithToken(t string) connector.Option    { return func(o *connector.Options) { o.Token = t } }
func WithTimeout(d time.Duration) connector.Option {
	return func(o *connector.Options) { o.DialTimeout = d }
}
