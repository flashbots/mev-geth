#!/usr/bin/bash

echo "package $GOPACKAGE" > artifacts.go
echo "" >> artifacts.go

cat artifacts/SimpleWallet.json | jq -c '{abi,bytecode}' | sed 's/.*/var simpleWalletArtifact = `&`/' >> artifacts.go
echo "" >> artifacts.go

cat artifacts/SingletonFactory.json | jq -c '{abi,bytecode}' | sed 's/.*/var singletonFactoryArtifact = `&`/' >> artifacts.go
echo "" >> artifacts.go

cat artifacts/TestCounter.json | jq -c '{abi,bytecode}' | sed 's/.*/var testCounterArtifact = `&`/' >> artifacts.go
echo "" >> artifacts.go

cat artifacts/TokenPaymaster.json | jq -c '{abi,bytecode}' | sed 's/.*/var tokenPaymasterArtifact = `&`/' >> artifacts.go
echo "" >> artifacts.go

gofmt -w artifacts.go
