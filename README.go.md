# Go 安全适配说明

初始化后请执行：

```bash
go mod tidy
go test ./...
```

要求：

- 必须提交 `go.mod` 和 `go.sum`。
- 新增依赖必须走 Issue + PR。
- CI 必须运行 gofmt / go vet / go test / govulncheck。
