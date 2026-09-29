.PHONY: test build frontend-test frontend-build backend-test backend-build deploy-verify release-version

test: frontend-test backend-test

build: frontend-build backend-build

frontend-test:
	cd frontend && npm test

frontend-build:
	cd frontend && npm run build

backend-test:
	cd backend && go test ./...

backend-build:
	cd backend && go build -o bin/bytemuse ./cmd/bytemuse

deploy-verify:
	pwsh -NoProfile -File deploy/verify.ps1

release-version:
	pwsh -NoProfile -File deploy/version.ps1
