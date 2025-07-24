module afetch

go 1.23.0

require (
	github.com/TWolfis/goapod v0.0.0
	github.com/ibmdb/go_ibm_db v0.5.2
	gopkg.in/yaml.v3 v3.0.1
)

require github.com/ibmruntimes/go-recordio/v2 v2.0.0-20240416213906-ae0ad556db70 // indirect

replace github.com/TWolfis/goapod => ../..
