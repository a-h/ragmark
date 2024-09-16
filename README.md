# github.com/a-h/ragmark

## Tasks

### sync

```bash
go run cmd/app/main.go sync
```

### chat

```bash
go run cmd/app/main.go chat -msg "How do I migrate from Jekyll?"
```

### hugo-serve

Dir: site

```bash
hugo serve
```

### gomod2nix-update

```bash
gomod2nix
```

### build

```bash
nix build
```

### run

```bash
nix run
```

### develop

```bash
nix develop
```

### docker-build

```bash
nix build .#docker-image
```

### docker-load

Once you've built the image, you can load it into a local Docker daemon with `docker load`.

```bash
docker load < result
```

### docker-run

```bash
docker run -p 8080:8080 app:latest
```

### sql-vec-download

```bash
#TODO: Get this done inside Nix, and use the correct architecture and OS for the download.
wget https://github.com/asg017/sqlite-vec/releases/download/v0.1.1/sqlite-vec-0.1.1-loadable-macos-aarch64.tar.gz -O sqlite-vec.tar.gz
```

### db-run

```bash
rqlited -auth=auth.json -extensions-path=sqlite-vec.tar.gz ~/strategy-node.1
```

### db-migration-create

```bash
migrate create -ext sql -dir db/migrations -seq create_documents_table
```

### go-run

```bash
go run ./cmd/app
```
