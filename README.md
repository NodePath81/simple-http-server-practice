# Go HTTP 文件服务器

## 简介
这是一个使用 Go 编写的简单 HTTP 文件服务器，并且不适用`net/http`包，支持静态文件服务、目录列表展示和 HTTP Range 请求。

## 特性
- 静态文件服务
- 自动生成目录列表页面（基于 HTML 模板）
- 支持 HTTP Range 请求
- 根据 HTTP 版本处理 Keep-Alive 连接

## 前提条件
- Go 语言环境 (建议 Go 1.18+)

## 编译与运行
1. 编译代码：
   ```bash
   go build -o server
