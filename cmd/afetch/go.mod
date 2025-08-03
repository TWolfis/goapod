module afetch

go 1.23.0

require (
	github.com/TWolfis/goapod v0.0.0
	github.com/go-sql-driver/mysql v1.9.3
	gopkg.in/yaml.v3 v3.0.1
)

require filippo.io/edwards25519 v1.1.0 // indirect

replace github.com/TWolfis/goapod => ../..
