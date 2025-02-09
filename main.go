package main

import (
	"bufio"
	"bytes"
	"flag"
	"fmt"
	"html/template"
	"io"
	"log"
	"mime"
	"net"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

var rootDir string

const templateStr = `
<!DOCTYPE html>
<html>
<head>
    <meta charset="UTF-8">
    <title>Directory Listing of {{.CurrentPath}}</title>
</head>
<body>
    <h1>Directory Listing of {{.CurrentPath}}</h1>
    <ul>
        {{range .Entries}}
            <li><a href="{{.URL}}">{{.Name}}</a></li>
        {{end}}
    </ul>
</body>
</html>
`

func init() {
	flag.StringVar(&rootDir, "d", ".", "工作目录")
}

func main() {
	listener, err := net.Listen("tcp", "localhost:8080")
	if err != nil {
		log.Fatal(err)
	}
	defer listener.Close()

	fmt.Println("Server listening on localhost:8080")

	for {
		conn, err := listener.Accept()
		if err != nil {
			log.Println("连接接受错误:", err)
			continue
		}
		go handleConnection(conn)
	}
}

// handleConnection 处理单个连接的 HTTP 请求
func handleConnection(conn net.Conn) {
	defer conn.Close()
	for {
		conn.SetReadDeadline(time.Now().Add(30 * time.Second))
		req, err := readHTTPRequest(conn)
		if err != nil {
			log.Println("读取 HTTP 请求错误:", err)
			break
		}
		keepAlive := shouldKeepAlive(req)
		if err := processHTTPRequest(conn, req, keepAlive); err != nil {
			log.Println("处理请求错误:", err)
			break
		}
		if !keepAlive {
			if tcpConn, ok := conn.(*net.TCPConn); ok {
				tcpConn.CloseWrite()
			}
			break
		}
	}
}

func getMimeType(filename string) string {
	ext := filepath.Ext(filename)
	mType := mime.TypeByExtension(ext)
	if mType == "" {
		mType = "application/octet-stream"
	}
	return mType
}

// getFile 根据请求路径返回对应文件或目录内容
func getFile(path string) (io.ReadSeeker, string) {
	cleanPath := filepath.Clean(path)
	fullPath := filepath.Join(rootDir, cleanPath)
	info, err := os.Stat(fullPath)
	if err != nil {
		log.Println("路径状态错误:", err)
		return nil, ""
	}

	if info.IsDir() {
		return generateDirectoryPage(fullPath, cleanPath), "text/html"
	}

	file, err := os.Open(fullPath)
	if err != nil {
		log.Println("文件打开错误:", err)
		return nil, ""
	}
	return file, getMimeType(fullPath)
}

type DirEntry struct {
	Name string
	URL  string
}

func listDirectoryEntries(dirPath, currentURL string) ([]DirEntry, error) {
	entries, err := os.ReadDir(dirPath)
	if err != nil {
		return nil, err
	}
	var result []DirEntry
	for _, entry := range entries {
		name := entry.Name()
		url := filepath.Join(currentURL, name)
		if entry.IsDir() {
			url += "/"
		}
		result = append(result, DirEntry{Name: name, URL: url})
	}
	return result, nil
}

type DirListingData struct {
	CurrentPath string
	Entries     []DirEntry
}

func generateDirectoryPage(fsDir, currentURL string) io.ReadSeeker {
	entries, err := listDirectoryEntries(fsDir, currentURL)
	if err != nil {
		return strings.NewReader("读取目录错误")
	}

	data := DirListingData{
		CurrentPath: currentURL,
		Entries:     entries,
	}

	var buf bytes.Buffer
	tmpl, err := template.New("dirList").Parse(templateStr)
	if err != nil {
		return strings.NewReader("模板解析错误")
	}

	if err := tmpl.Execute(&buf, data); err != nil {
		return strings.NewReader("生成页面错误")
	}

	return bytes.NewReader(buf.Bytes())
}

// processHTTPRequest 仅处理 GET 请求
func processHTTPRequest(conn net.Conn, req *HTTPRequest, keepAlive bool) error {
	if req.Method != "GET" {
		resp := "HTTP/1.1 405 Method Not Allowed\r\nContent-Length: 0\r\nConnection: close\r\n\r\n"
		conn.Write([]byte(resp))
		return nil
	}

	fileRS, mimeType := getFile(req.Path)
	if fileRS == nil {
		resp := "HTTP/1.1 404 Not Found\r\nContent-Length: 0\r\nConnection: close\r\n\r\n"
		conn.Write([]byte(resp))
		return nil
	}

	if _, ok := req.Headers["range"]; ok {
		return handleRangeRequest(conn, req, fileRS, mimeType, keepAlive)
	}

	content, err := io.ReadAll(fileRS)
	if err != nil {
		resp := "HTTP/1.1 500 Internal Server Error\r\nContent-Length: 0\r\nConnection: close\r\n\r\n"
		conn.Write([]byte(resp))
		return err
	}

	connState := "keep-alive"
	if !keepAlive {
		connState = "close"
	}
	header := fmt.Sprintf("HTTP/1.1 200 OK\r\nContent-Length: %d\r\nContent-Type: %s\r\nConnection: %s\r\n\r\n",
		len(content), mimeType, connState)
	conn.Write([]byte(header))
	conn.Write(content)
	return nil
}

type HTTPRequest struct {
	Method  string
	Path    string
	Version string
	Headers map[string]string
}

func readHTTPRequest(conn net.Conn) (*HTTPRequest, error) {
	reader := bufio.NewReader(conn)

	// 读取请求行，如 "GET /path HTTP/1.1"
	line, err := reader.ReadString('\n')
	if err != nil {
		return nil, err
	}
	line = strings.TrimSpace(line)
	parts := strings.Split(line, " ")
	if len(parts) < 3 {
		return nil, fmt.Errorf("无效的请求行")
	}
	req := &HTTPRequest{
		Method:  parts[0],
		Path:    parts[1],
		Version: parts[2],
		Headers: make(map[string]string),
	}

	// 读取请求头
	for {
		headerLine, err := reader.ReadString('\n')
		if err != nil {
			return nil, err
		}
		headerLine = strings.TrimSpace(headerLine)
		if headerLine == "" {
			break
		}
		headerParts := strings.SplitN(headerLine, ":", 2)
		if len(headerParts) == 2 {
			key := strings.ToLower(strings.TrimSpace(headerParts[0]))
			value := strings.TrimSpace(headerParts[1])
			req.Headers[key] = value
		}
	}
	return req, nil
}

func shouldKeepAlive(req *HTTPRequest) bool {
	connectionHeader, exists := req.Headers["connection"]

	if req.Version == "HTTP/1.1" {
		return !(exists && strings.ToLower(connectionHeader) == "close")
	} else if req.Version == "HTTP/1.0" {
		return exists && strings.ToLower(connectionHeader) == "keep-alive"
	}
	return false
}

func parseRangeHeader(rangeHeader string, fileSize int64) (start, end int64, err error) {
	const prefix = "bytes="
	if !strings.HasPrefix(rangeHeader, prefix) {
		return 0, 0, fmt.Errorf("无效的 Range 头")
	}
	spec := strings.TrimPrefix(rangeHeader, prefix)
	parts := strings.Split(spec, "-")
	if len(parts) != 2 {
		return 0, 0, fmt.Errorf("无效的 Range 规范")
	}
	if parts[0] == "" { // 后缀范围，如 "-500"
		suffix, err := strconv.ParseInt(parts[1], 10, 64)
		if err != nil {
			return 0, 0, err
		}
		if suffix > fileSize {
			suffix = fileSize
		}
		return fileSize - suffix, fileSize - 1, nil
	}
	start, err = strconv.ParseInt(parts[0], 10, 64)
	if err != nil {
		return 0, 0, err
	}
	if parts[1] != "" {
		end, err = strconv.ParseInt(parts[1], 10, 64)
		if err != nil {
			return 0, 0, err
		}
	} else {
		end = fileSize - 1
	}
	if start > end || end >= fileSize {
		return 0, 0, fmt.Errorf("无效的 Range: start=%d, end=%d, fileSize=%d", start, end, fileSize)
	}
	return start, end, nil
}

func handleRangeRequest(conn net.Conn, req *HTTPRequest, rs io.ReadSeeker, mimeType string, keepAlive bool) error {
	file, ok := rs.(*os.File)
	if !ok {
		resp := "HTTP/1.1 416 Range Not Satisfiable\r\nContent-Length: 0\r\nConnection: close\r\n\r\n"
		conn.Write([]byte(resp))
		return fmt.Errorf("不支持 Range 请求")
	}
	fi, err := file.Stat()
	if err != nil {
		return err
	}
	fileSize := fi.Size()

	rangeHeader := req.Headers["range"]
	start, end, err := parseRangeHeader(rangeHeader, fileSize)
	if err != nil {
		resp := "HTTP/1.1 416 Range Not Satisfiable\r\nContent-Length: 0\r\nConnection: close\r\n\r\n"
		conn.Write([]byte(resp))
		return err
	}
	length := end - start + 1

	_, err = rs.Seek(start, io.SeekStart)
	if err != nil {
		return err
	}
	content := make([]byte, length)
	n, err := io.ReadFull(rs, content)
	if err != nil || int64(n) != length {
		return fmt.Errorf("读取 Range 内容失败")
	}

	connState := "keep-alive"
	if !keepAlive {
		connState = "close"
	}
	header := fmt.Sprintf("HTTP/1.1 206 Partial Content\r\nContent-Length: %d\r\nContent-Type: %s\r\n", len(content), mimeType)
	header += fmt.Sprintf("Content-Range: bytes %d-%d/%d\r\nConnection: %s\r\n\r\n", start, end, fileSize, connState)
	conn.Write([]byte(header))
	conn.Write(content)
	return nil
}
