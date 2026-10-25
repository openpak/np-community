Copy of the account core's generated gRPC stubs and `.proto` sources.
Refresh from `platforms/core/infra/account/proto/openpak/account/v1` at the workspace
root. Preserve the Go package `openpak/np-common/accountpb` when regenerating;
consumers vendor this module and need no account-core checkout during builds.
