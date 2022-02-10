#!/usr/bin/bash

echo "package $GOPACKAGE" > artifact.go
echo "" >> artifact.go

cat EntryPoint.json | jq -c '{abi,bytecode}' | sed 's/.*/var entryPointArtifact = `&`/' >> artifact.go
echo "" >> artifact.go

gofmt -w artifact.go
