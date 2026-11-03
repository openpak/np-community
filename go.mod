module openpak/np-community

go 1.26.0

require (
	github.com/jackc/pgx/v5 v5.11.0
	github.com/openpak/account-api v0.1.0
	google.golang.org/grpc v1.83.2
	google.golang.org/protobuf v1.36.11
	openpak/np-common v0.0.0
)

require (
	github.com/jackc/pgpassfile v1.0.0 // indirect
	github.com/jackc/pgservicefile v0.0.0-20240606120523-5a60cdf6a761 // indirect
	github.com/jackc/puddle/v2 v2.2.2 // indirect
	golang.org/x/net v0.58.0 // indirect
	golang.org/x/sync v0.22.0 // indirect
	golang.org/x/sys v0.47.0 // indirect
	golang.org/x/text v0.41.0 // indirect
	google.golang.org/genproto/googleapis/rpc v0.0.0-20260526163538-3dc84a4a5aaa // indirect
)

replace openpak/np-common => ../np-common
