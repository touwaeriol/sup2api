module github.com/Sub2API-Devs/sup2api/next/sdk

go 1.27

require (
	github.com/Masterminds/semver/v3 v3.5.0
	github.com/Sub2API-Devs/sup2api/next/runtime-contract v0.0.0
	github.com/aws/aws-sdk-go-v2 v1.48.0
	github.com/aws/aws-sdk-go-v2/aws/protocol/eventstream v1.7.27
	github.com/aws/smithy-go v1.28.9
	github.com/fergusstrange/embedded-postgres v1.34.0
	github.com/hashicorp/go-hclog v1.6.3
	github.com/hashicorp/go-plugin v1.8.0
	github.com/robfig/cron/v3 v3.0.1
	github.com/stretchr/testify v1.11.1
	github.com/tidwall/gjson v1.19.0
	github.com/tidwall/sjson v1.2.5
	golang.org/x/net v0.58.0
	golang.org/x/sync v0.23.0
	golang.org/x/sys v0.48.0
	google.golang.org/grpc v1.84.0
	google.golang.org/protobuf v1.36.12
	gorm.io/driver/postgres v1.6.3
	gorm.io/gorm v1.31.2
)

require (
	github.com/davecgh/go-spew v1.1.1 // indirect
	github.com/fatih/color v1.13.0 // indirect
	github.com/golang/protobuf v1.5.4 // indirect
	github.com/hashicorp/yamux v0.1.2 // indirect
	github.com/jackc/pgpassfile v1.0.0 // indirect
	github.com/jackc/pgservicefile v0.0.0-20240606120523-5a60cdf6a761 // indirect
	github.com/jackc/pgx/v5 v5.11.0 // indirect
	github.com/jackc/puddle/v2 v2.2.2 // indirect
	github.com/jinzhu/inflection v1.0.0 // indirect
	github.com/jinzhu/now v1.1.5 // indirect
	github.com/lib/pq v1.10.9 // indirect
	github.com/mattn/go-colorable v0.1.12 // indirect
	github.com/mattn/go-isatty v0.0.20 // indirect
	github.com/oklog/run v1.1.0 // indirect
	github.com/pmezard/go-difflib v1.0.0 // indirect
	github.com/rogpeppe/go-internal v1.16.0 // indirect
	github.com/tidwall/match v1.1.1 // indirect
	github.com/tidwall/pretty v1.2.0 // indirect
	github.com/xi2/xz v0.0.0-20171230120015-48954b6210f8 // indirect
	golang.org/x/text v0.42.0 // indirect
	google.golang.org/genproto/googleapis/rpc v0.0.0-20260706201446-f0a921348800 // indirect
	gopkg.in/yaml.v3 v3.0.1 // indirect
)

replace github.com/Sub2API-Devs/sup2api/next/runtime-contract => ../runtime-contract
