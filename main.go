package main

import (
	"flag"
	"fmt"
	"github.com/jackpal/gateway"
	"github.com/mdp/qrterminal/v3"
	"io"
	"log"
	"net"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"text/tabwriter"
)

// Global variables
var (
	allowSingleFilePath string   // Single file allowed (via -f)
	allowMultiFilePaths []string // Multiple files allowed (via -x, comma-separated)
	currentWorkDir      string   // Current working directory (absolute path)
	showHelp            bool     // Show help information (via -h)
)

// DownloadFileInfo represents file info for download list page
type DownloadFileInfo struct {
	FileName string // Just the filename (e.g., test.txt)
	RelPath  string // Relative path to current dir (e.g., uploads/test.txt)
	AbsPath  string // Absolute path (e.g., /home/user/app/uploads/test.txt)
	Size     int64  // File size in bytes
	Exists   bool   // Whether the file exists
}

// Use ascii blocks to form the QR Code
const BLACK_WHITE = "▄"
const BLACK_BLACK = " "
const WHITE_BLACK = "▀"
const WHITE_WHITE = "█"

// localIPString adds error return value to expose internal errors to upper layer processing
// Return values: localIP(string), error
func localIPString() (string, error) {
	// Discover the default gateway's IP address
	gwIP, err := gateway.DiscoverGateway()
	if err != nil {
		// No longer directly Fatal, but return error for upper layer processing
		return "", fmt.Errorf("failed to discover gateway: %w", err)
	}

	// Find the local IP address associated with the interface that connects to the gateway
	localIP, err := getLocalIPForGateway(gwIP)
	if err != nil {
		return "", fmt.Errorf("failed to find local IP for gateway: %w", err)
	}

	// Additional validation: prevent returning nil IP
	if localIP == nil {
		return "", fmt.Errorf("local IP address is nil")
	}

	return localIP.String(), nil
}

// getLocalIPForGateway finds the local IP that is in the same subnet as the gateway IP
func getLocalIPForGateway(gwIP net.IP) (net.IP, error) {
	interfaces, err := net.Interfaces()
	if err != nil {
		return nil, fmt.Errorf("failed to retrieve network interfaces: %w", err)
	}

	for _, iface := range interfaces {
		// Skip disabled network cards
		if iface.Flags&net.FlagUp == 0 {
			continue
		}

		addrs, err := iface.Addrs()
		if err != nil {
			// Record warning for single network card address acquisition failure, do not interrupt overall process
			log.Printf("Warning: failed to get addresses for interface %s: %v", iface.Name, err)
			continue
		}

		for _, addr := range addrs {
			ipnet, ok := addr.(*net.IPNet)
			if !ok {
				continue
			}

			// Only keep IPv4 addresses, and filter loopback/non-global unicast addresses
			ipv4 := ipnet.IP.To4()
			if ipv4 == nil || !ipv4.IsGlobalUnicast() || ipv4.IsLoopback() {
				continue
			}

			// Check if the gateway is in the subnet of the current network card
			if ipnet.Contains(gwIP) {
				return ipv4, nil
			}
		}
	}

	return nil, fmt.Errorf("no local IPv4 address found in the same subnet as gateway %s", gwIP.String())
}

// uploadFormHandler returns the HTML page with file upload form and progress bar
func uploadFormHandler(w http.ResponseWriter, r *http.Request) {
	// Only match GET requests for the root path
	if r.Method != http.MethodGet || r.URL.Path != "/" {
		http.NotFound(w, r)
		return
	}

	// HTML page with progress bar and JS upload logic (responsive design)
	html := `
<!DOCTYPE html>
<html lang="en">
<head>
    <meta charset="UTF-8">
    <meta name="viewport" content="width=device-width, initial-scale=1.0">
    <title>Upload files</title>
    <style>
        /* Reset default styles */
        * {
            margin: 0;
            padding: 0;
            box-sizing: border-box;
        }
        
        body {
            max-width: 600px;
            margin: 0 auto;
            padding: 20px 15px;
            font-family: -apple-system, BlinkMacSystemFont, 'Segoe UI', Arial, sans-serif;
            line-height: 1.5;
        }
        
        .upload-box {
            padding: 25px 15px;
            border: 2px dashed #ccc;
            border-radius: 8px;
            text-align: center;
            width: 100%;
        }
        
        h1 {
            font-size: 1.8rem;
            margin-bottom: 20px;
            color: #333;
        }
        
        #fileInput {
            margin: 20px 0;
            padding: 10px;
            width: 100%;
            font-size: 1rem;
        }
        
        #uploadBtn {
            padding: 12px 30px;
            background-color: #4285f4;
            color: white;
            border: none;
            border-radius: 4px;
            cursor: pointer;
            font-size: 1rem;
            margin-bottom: 20px;
            width: 100%;
            max-width: 300px;
        }
        
        #uploadBtn:hover {
            background-color: #3367d6;
        }
        
        #uploadBtn:disabled {
            background-color: #9aa0a6;
            cursor: not-allowed;
        }
        
        .progress-container {
            width: 100%;
            height: 20px;
            border: 1px solid #ccc;
            border-radius: 10px;
            margin: 20px 0;
            overflow: hidden;
            display: none;
        }
        
        .progress-bar {
            height: 100%;
            width: 0%;
            background-color: #28a745;
            transition: width 0.2s ease;
            border-radius: 10px;
        }
        
        #progressText {
            color: #666;
            font-size: 0.9rem;
            display: none;
            margin-bottom: 15px;
        }
        
        #result {
            margin-top: 20px;
            padding: 15px;
            border-radius: 4px;
            display: none;
            font-size: 0.95rem;
        }
        
        .success {
            color: #28a745;
            border: 1px solid #28a745;
            background-color: #f8fff9;
        }
        
        .error {
            color: #dc3545;
            border: 1px solid #dc3545;
            background-color: #fff5f5;
        }
        
        #backBtn {
            display: none;
            margin-top: 20px;
            padding: 10px 20px;
            color: #4285f4;
            border: 1px solid #4285f4;
            border-radius: 4px;
            background: white;
            cursor: pointer;
            text-decoration: none;
            font-size: 0.9rem;
        }
        
        .download-link {
            color: #4285f4;
            font-size: 0.9rem;
            margin-top: 20px;
            display: block;
            text-decoration: none;
        }

        /* Media queries for larger screens */
        @media (min-width: 480px) {
            h1 {
                font-size: 2rem;
            }
            
            .upload-box {
                padding: 30px;
            }
        }
    </style>
</head>
<body>
    <div class="upload-box">
        <h1>Upload files</h1>
        <input type="file" id="fileInput" name="files" multiple accept="*/*">
        <br>
        <button id="uploadBtn" onclick="uploadFiles()">Upload</button>
        
        <!-- Progress bar container -->
        <div class="progress-container" id="progressContainer">
            <div class="progress-bar" id="progressBar"></div>
        </div>
        <div id="progressText">Upload Progress: 0%</div>
        
        <!-- Upload result display -->
        <div id="result"></div>
        <a id="backBtn" href="/">Back to Upload page</a>
        <a href="/downloads" class="download-link">📌 Go to Download List Page</a>
    </div>

    <script>
        // Global variable
        let xhr;

        // Core file upload function
        function uploadFiles() {
            const fileInput = document.getElementById('fileInput');
            const files = fileInput.files;
            const uploadBtn = document.getElementById('uploadBtn');
            const progressContainer = document.getElementById('progressContainer');
            const progressBar = document.getElementById('progressBar');
            const progressText = document.getElementById('progressText');
            const result = document.getElementById('result');
            const backBtn = document.getElementById('backBtn');

            // Validate if files are selected
            if (files.length === 0) {
                showResult('Please select at least one file!', 'error');
                return;
            }

            // Disable upload button and show progress bar
            uploadBtn.disabled = true;
            progressContainer.style.display = 'block';
            progressText.style.display = 'block';
            result.style.display = 'none';
            backBtn.style.display = 'none';

            // Build FormData (match server field name)
            const formData = new FormData();
            for (let i = 0; i < files.length; i++) {
                formData.append('files', files[i]);
            }

            // Create XHR object and listen to upload progress
            xhr = new XMLHttpRequest();
            xhr.open('POST', '/upload', true);

            // Listen to progress event (core: get upload progress)
            xhr.upload.addEventListener('progress', function(e) {
                if (e.lengthComputable) {
                    // Calculate progress percentage
                    const percent = Math.round((e.loaded / e.total) * 100);
                    progressBar.style.width = percent + '%';
                    progressText.textContent = 'Upload Progress: ' + percent + '%';
                }
            });

            // Listen to upload completion
            xhr.addEventListener('load', function() {
                if (xhr.status >= 200 && xhr.status < 300) {
                    // Upload success
                    showResult(xhr.responseText, 'success');
                } else {
                    // Upload failed
                    showResult('Upload failed: ' + xhr.statusText, 'error');
                }
                resetUI();
            });

            // Listen to upload error
            xhr.addEventListener('error', function() {
                showResult('Upload failed: Network error', 'error');
                resetUI();
            });

            // Listen to upload abort
            xhr.addEventListener('abort', function() {
                showResult('Upload cancelled', 'error');
                resetUI();
            });

            // Send request
            xhr.send(formData);
        }

        // Show upload result
        function showResult(msg, type) {
            const result = document.getElementById('result');
            const backBtn = document.getElementById('backBtn');
            result.textContent = msg;
            result.className = type;
            result.style.display = 'block';
            backBtn.style.display = 'inline-block';
        }

        // Reset UI state
        function resetUI() {
            const uploadBtn = document.getElementById('uploadBtn');
            uploadBtn.disabled = false;
        }

        // Cancel upload (optional: use when adding cancel button)
        function cancelUpload() {
            if (xhr) {
                xhr.abort();
            }
        }
    </script>
</body>
</html>
`
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	fmt.Fprint(w, html)
}

// uploadHandler handles file upload requests
func uploadHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "Only POST method is supported", http.StatusMethodNotAllowed)
		return
	}

	// Parse multipart/form-data (no size limit)
	err := r.ParseMultipartForm(0) // 0 means no limit on memory buffer size
	if err != nil {
		http.Error(w, fmt.Sprintf("Failed to parse form: %v", err), http.StatusBadRequest)
		return
	}

	files := r.MultipartForm.File["files"]
	if len(files) == 0 {
		http.Error(w, "No files were uploaded", http.StatusBadRequest)
		return
	}

	// Create save directory (under current working directory)
	//saveDir := filepath.Join(currentWorkDir, "uploads")
	saveDir := currentWorkDir
	if err := os.MkdirAll(saveDir, 0755); err != nil {
		http.Error(w, fmt.Sprintf("Failed to create save directory: %v", err), http.StatusInternalServerError)
		return
	}

	// Iterate and save files
	var uploadedFiles []string
	buf := make([]byte, 1024*1024) // 1MB buffer
	for _, fileHeader := range files {
		file, err := fileHeader.Open()
		if err != nil {
			http.Error(w, fmt.Sprintf("Failed to open file %s: %v", fileHeader.Filename, err), http.StatusInternalServerError)
			return
		}
		defer file.Close()

		savePath := filepath.Join(saveDir, fileHeader.Filename)
		// Check if file exists to avoid overwriting
		if _, err := os.Stat(savePath); err == nil {
			http.Error(w, fmt.Sprintf("File %s already exists", fileHeader.Filename), http.StatusConflict)
			return
		}

		dstFile, err := os.Create(savePath)
		if err != nil {
			http.Error(w, fmt.Sprintf("Failed to create file %s: %v", fileHeader.Filename, err), http.StatusInternalServerError)
			return
		}
		defer dstFile.Close()

		// Write file in chunks
		for {
			n, err := file.Read(buf)
			if n > 0 {
				if _, err := dstFile.Write(buf[:n]); err != nil {
					http.Error(w, fmt.Sprintf("Failed to write file %s: %v", fileHeader.Filename, err), http.StatusInternalServerError)
					return
				}
			}
			if err == io.EOF {
				break
			}
			if err != nil {
				http.Error(w, fmt.Sprintf("Failed to read file %s: %v", fileHeader.Filename, err), http.StatusInternalServerError)
				return
			}
		}

		// Set file permissions
		if err := os.Chmod(savePath, 0644); err != nil {
			fmt.Printf("Failed to set permissions for file %s: %v\n", savePath, err)
		}

		uploadedFiles = append(uploadedFiles, fileHeader.Filename)
	}

	// Return upload success response
	w.WriteHeader(http.StatusOK)
	responseMsg := fmt.Sprintf("Successfully uploaded %d files: %s", len(uploadedFiles), strings.Join(uploadedFiles, ", "))
	fmt.Fprint(w, responseMsg)
}

// getDownloadableFiles returns list of downloadable files (from -f or -x)
func getDownloadableFiles() []DownloadFileInfo {
	var files []DownloadFileInfo

	// Priority: -f (single file) first, then -x (multiple files)
	if allowSingleFilePath != "" {
		absPath := filepath.Clean(filepath.Join(currentWorkDir, allowSingleFilePath))
		fileInfo := getFileInfo(allowSingleFilePath, absPath)
		files = append(files, fileInfo)
	} else if len(allowMultiFilePaths) > 0 {
		// Process all files from -x parameter
		for _, relPath := range allowMultiFilePaths {
			absPath := filepath.Clean(filepath.Join(currentWorkDir, relPath))
			fileInfo := getFileInfo(relPath, absPath)
			files = append(files, fileInfo)
		}
	}

	return files
}

// getFileInfo returns DownloadFileInfo for a given path
func getFileInfo(relPath, absPath string) DownloadFileInfo {
	fileInfo := DownloadFileInfo{
		FileName: filepath.Base(absPath),
		RelPath:  relPath,
		AbsPath:  absPath,
		Exists:   false,
	}

	// Check if file exists and get size
	stat, err := os.Stat(absPath)
	if err == nil && !stat.IsDir() {
		fileInfo.Exists = true
		fileInfo.Size = stat.Size()
	}

	return fileInfo
}

// formatFileSize converts bytes to human-readable format (B, KB, MB, GB)
func formatFileSize(bytes int64) string {
	const unit = 1024
	if bytes < unit {
		return fmt.Sprintf("%d B", bytes)
	}
	div, exp := int64(unit), 0
	for n := bytes / unit; n >= unit; n /= unit {
		div *= unit
		exp++
	}
	return fmt.Sprintf("%.1f %cB", float64(bytes)/float64(div), "KMGTPE"[exp])
}

// downloadsListHandler shows the list of downloadable files (responsive design, simplified)
func downloadsListHandler(w http.ResponseWriter, r *http.Request) {
	// Only match GET requests for /downloads
	if r.Method != http.MethodGet || r.URL.Path != "/downloads" {
		http.NotFound(w, r)
		return
	}

	// Get downloadable files list
	files := getDownloadableFiles()
	totalFiles := len(files)

	// Generate HTML for download list (simplified, no stats/path/status)
	html := `
<!DOCTYPE html>
<html lang="en">
<head>
    <meta charset="UTF-8">
    <meta name="viewport" content="width=device-width, initial-scale=1.0">
    <title>Download Files List</title>
    <style>
        /* Reset default styles */
        * {
            margin: 0;
            padding: 0;
            box-sizing: border-box;
        }
        
        body {
            max-width: 900px;
            margin: 0 auto;
            padding: 20px 15px;
            font-family: -apple-system, BlinkMacSystemFont, 'Segoe UI', Arial, sans-serif;
            line-height: 1.5;
        }
        
        .list-container {
            padding: 20px 15px;
            border: 1px solid #eee;
            border-radius: 8px;
            width: 100%;
        }
        
        h1 {
            font-size: 1.8rem;
            color: #333;
            text-align: center;
            margin-bottom: 20px;
        }
        
        /* Table container for horizontal scroll on mobile */
        .table-container {
            overflow-x: auto;
            margin: 20px 0;
        }
        
        table {
            width: 100%;
            min-width: 300px;
            border-collapse: collapse;
        }
        
        th, td {
            padding: 12px 8px;
            text-align: left;
            border-bottom: 1px solid #ddd;
            font-size: 0.9rem;
        }
        
        th {
            background-color: #f8f9fa;
            position: sticky;
            top: 0;
            font-weight: 600;
        }
        
        /* Column width adjustments (only Filename, Size, Action) */
        th:nth-child(1), td:nth-child(1) { width: 60%; } /* Filename */
        th:nth-child(2), td:nth-child(2) { width: 20%; } /* Size */
        th:nth-child(3), td:nth-child(3) { width: 20%; } /* Action */
        
        .download-btn {
            padding: 8px 12px;
            background-color: #28a745;
            color: white;
            border: none;
            border-radius: 4px;
            cursor: pointer;
            text-decoration: none;
            font-size: 0.85rem;
            display: inline-block;
            width: 100%;
            text-align: center;
        }
        
        .download-btn:hover {
            background-color: #218838;
        }
        
        .download-btn:disabled {
            background-color: #6c757d;
            cursor: not-allowed;
        }
        
        .back-link {
            display: inline-block;
            margin-top: 20px;
            color: #4285f4;
            text-decoration: none;
            font-size: 0.95rem;
        }
        
        .empty-message {
            text-align: center;
            color: #666;
            font-size: 1rem;
            margin: 40px 0;
            padding: 20px;
            border: 1px dashed #ddd;
            border-radius: 4px;
        }

        /* Media queries for smaller screens */
        @media (max-width: 480px) {
            h1 {
                font-size: 1.5rem;
            }
            
            th, td {
                padding: 10px 6px;
                font-size: 0.85rem;
            }
            
            .download-btn {
                padding: 6px 8px;
                font-size: 0.8rem;
            }
        }
    </style>
</head>
<body>
    <div class="list-container">
        <h1>Downloadable Files</h1>
        <a href="/" class="back-link">← Back to Upload</a>
    `

	// Add files table or empty message
	if totalFiles == 0 {
		html += `<div class="empty-message">No downloadable files configured (use -f or -x parameter)</div>`
	} else {
		html += `
        <div class="table-container">
            <table>
                <tr>
                    <th>Filename</th>
                    <th>Size</th>
                    <th>Action</th>
                </tr>
        `
		// Add all files from -x (or -f) to table (only filename, size, action)
		for _, file := range files {
			btnDisabled := "disabled"
			btnHref := ""

			if file.Exists {
				btnDisabled = ""
				// Encode relative path for URL (supports spaces/special chars)
				encodedPath := url.PathEscape(file.RelPath)
				btnHref = fmt.Sprintf("/download/%s", encodedPath)
			}

			// Add row for each file (only filename, size, download button)
			html += fmt.Sprintf(`
            <tr>
                <td>%s</td>
                <td>%s</td>
                <td>
                    <a href="%s" class="download-btn" %s>Download</a>
                </td>
            </tr>
            `, file.FileName, formatFileSize(file.Size), btnHref, btnDisabled)
		}
		html += `</table></div>`
	}

	html += `
    </div>
</body>
</html>
`

	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	fmt.Fprint(w, html)
}

// downloadHandler handles file download requests (ONLY current directory files)
func downloadHandler(w http.ResponseWriter, r *http.Request) {
	// Only handle GET method
	if r.Method != http.MethodGet {
		http.Error(w, "Only GET method is supported", http.StatusMethodNotAllowed)
		return
	}

	// 1. Extract raw path after /download/ and decode URL
	rawPath := strings.TrimPrefix(r.URL.Path, "/download/")
	if rawPath == "" {
		http.Error(w, fmt.Sprintf("Please specify relative path (under %s) e.g., /download/uploads/test.txt", currentWorkDir), http.StatusBadRequest)
		return
	}

	// Decode URL-encoded path
	decodedPath, err := url.PathUnescape(rawPath)
	if err != nil {
		http.Error(w, fmt.Sprintf("Failed to decode file path: %v", err), http.StatusBadRequest)
		return
	}

	// 2. Resolve to absolute path under current working directory (FORBID absolute/parent paths)
	targetPath := filepath.Join(currentWorkDir, decodedPath)
	// Clean path to remove ../ or ./
	cleanTargetPath := filepath.Clean(targetPath)

	// 3. Critical check: ensure the file is within current working directory
	relPath, err := filepath.Rel(currentWorkDir, cleanTargetPath)
	if err != nil || strings.HasPrefix(relPath, "..") {
		http.Error(w, fmt.Sprintf("Access denied: File must be within current directory (%s)", currentWorkDir), http.StatusForbidden)
		return
	}

	// 4. Check if file is in allowed list (supports multiple files from -x)
	allowed := false
	downloadableFiles := getDownloadableFiles()
	for _, file := range downloadableFiles {
		if file.AbsPath == cleanTargetPath && file.Exists {
			allowed = true
			break
		}
	}
	if !allowed {
		http.Error(w, "Access denied: File is not in allowed download list", http.StatusForbidden)
		return
	}

	// 5. Check if file exists (double check)
	fileInfo, err := os.Stat(cleanTargetPath)
	if err != nil {
		if os.IsNotExist(err) {
			http.Error(w, fmt.Sprintf("File %s does not exist (under %s)", decodedPath, currentWorkDir), http.StatusNotFound)
		} else {
			http.Error(w, fmt.Sprintf("Failed to get file information: %v", err), http.StatusInternalServerError)
		}
		return
	}

	// Forbid directory download
	if fileInfo.IsDir() {
		http.Error(w, fmt.Sprintf("%s is a directory, download is not supported", decodedPath), http.StatusBadRequest)
		return
	}

	// 6. Open file (only within current directory)
	file, err := os.Open(cleanTargetPath)
	if err != nil {
		http.Error(w, fmt.Sprintf("Failed to open file: %v", err), http.StatusInternalServerError)
		return
	}
	defer file.Close()

	// 7. Set download response headers
	fileName := filepath.Base(cleanTargetPath)
	w.Header().Set("Content-Type", "application/octet-stream")
	w.Header().Set("Content-Disposition", fmt.Sprintf("attachment; filename=\"%s\"", fileName))
	w.Header().Set("Content-Length", strconv.FormatInt(fileInfo.Size(), 10))

	// 8. Stream file in chunks
	buf := make([]byte, 1024*1024)
	for {
		n, err := file.Read(buf)
		if n > 0 {
			_, writeErr := w.Write(buf[:n])
			if writeErr != nil {
				fmt.Printf("Failed to write download response: %v\n", writeErr)
				return
			}
			// Flush to ensure real-time transmission
			if flusher, ok := w.(http.Flusher); ok {
				flusher.Flush()
			}
		}
		if err == io.EOF {
			break
		}
		if err != nil {
			fmt.Printf("Failed to read download file: %v\n", err)
			http.Error(w, "Failed to read file", http.StatusInternalServerError)
			return
		}
	}
}

// printHelp shows help information
func printHelp() {
	writer := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)
	fmt.Fprintln(writer, "CLI to transfer files between PC and mobile via QR code scanning.")
	fmt.Fprintln(writer, "=============================")
	fmt.Fprintln(writer, "Usage:")
	fmt.Fprintln(writer, "  pair [OPTIONS]")
	fmt.Fprintln(writer, "")
	fmt.Fprintln(writer, "Options:")
	fmt.Fprintln(writer, "  -h\t\tShow this help message and exit")
	fmt.Fprintln(writer, "  -f PATH\tSpecify single file to allow download (relative to current dir)")
	fmt.Fprintln(writer, "  -x PATHS\tSpecify multiple files to allow download (comma-separated, no spaces)")
	fmt.Fprintln(writer, "\t\t  Example: -x uploads/file1.txt,uploads/file2.pdf,docs/readme.md,data/file3.zip")
	fmt.Fprintln(writer, "")
	fmt.Fprintln(writer, "Access:")
	fmt.Fprintln(writer, "  Upload Page: http://localhost:8080")
	fmt.Fprintln(writer, "  Download List: http://localhost:8080/downloads (shows all downloadable files)")
	fmt.Fprintln(writer, "  Direct Download: http://localhost:8080/download/[filename]")
	writer.Flush()
}

func main() {
	// Parse command line flags
	flag.BoolVar(&showHelp, "h", false, "Show help information")
	flag.StringVar(&allowSingleFilePath, "f", "", "Single file to allow download (relative to current dir)")
	var multiFilesStr string
	flag.StringVar(&multiFilesStr, "x", "", "Multiple files to allow download (comma-separated, relative to current dir)")
	flag.Parse()

	// Show help if -h is specified
	if showHelp {
		printHelp()
		return
	}

	// Parse -x parameter (split comma-separated paths, support ANY number of files)
	if multiFilesStr != "" {
		// Split by comma, trim whitespace, remove empty entries
		paths := strings.Split(multiFilesStr, ",")
		for _, p := range paths {
			cleanPath := strings.TrimSpace(p)
			if cleanPath != "" {
				allowMultiFilePaths = append(allowMultiFilePaths, cleanPath)
			}
		}

		// Remove duplicate paths (optional but useful)
		uniquePaths := make(map[string]bool)
		var uniqueList []string
		for _, p := range allowMultiFilePaths {
			if !uniquePaths[p] {
				uniquePaths[p] = true
				uniqueList = append(uniqueList, p)
			}
		}
		allowMultiFilePaths = uniqueList

		// Show number of files configured from -x
		fmt.Printf("- Configured %d files for download via -x parameter\n", len(allowMultiFilePaths))
	}

	// Validate parameters (only one of -f or -x can be used)
	if allowSingleFilePath != "" && len(allowMultiFilePaths) > 0 {
		fmt.Println("Error: Only one of -f (single file) or -x (multiple files) can be used")
		os.Exit(1)
	}

	// Get current working directory (absolute path)
	var err error
	currentWorkDir, err = os.Getwd()
	if err != nil {
		fmt.Printf("Failed to get current working directory: %v\n", err)
		os.Exit(1)
	}
	currentWorkDir = filepath.Clean(currentWorkDir) // Ensure clean absolute path

	// Register routes (no conflict)
	http.HandleFunc("/", uploadFormHandler)             // Root path: upload page
	http.HandleFunc("/upload", uploadHandler)           // Upload API
	http.HandleFunc("/downloads", downloadsListHandler) // Download list page (simplified)
	http.HandleFunc("/download/", downloadHandler)      // Download API (fixed prefix)

	// Call the modified localIPString, receive IP and error return values
	localIP, err := localIPString()
	if err != nil {
		log.Fatalf("Failed to get local IP address: %v", err)
	}
	fmt.Printf("Local IP address: %s\n", localIP)

	// Server startup messages
	fmt.Printf("Server started, current working directory: %s\n", currentWorkDir)
	fmt.Printf("- Upload Page: http://%s:8080\n", localIP)

	// Show allowed files info
	if allowSingleFilePath != "" {
		allowedAbsPath := filepath.Clean(filepath.Join(currentWorkDir, allowSingleFilePath))
		fmt.Printf("- Allowed download file: %s (absolute: %s)\n", allowSingleFilePath, allowedAbsPath)
		fmt.Printf("  Direct download URL: http://%s:8080/download/%s\n", localIP, allowSingleFilePath)
	} else if len(allowMultiFilePaths) > 0 {
		fmt.Printf("- Download List Page: http://%s:8080/downloads (shows all configured files)\n", localIP)
		fmt.Printf("- Allowed download files (total: %d):\n", len(allowMultiFilePaths))
		for i, p := range allowMultiFilePaths {
			absPath := filepath.Clean(filepath.Join(currentWorkDir, p))
			fmt.Printf("  %d. %s (absolute: %s)\n", i+1, p, absPath)
			fmt.Printf("     Direct download URL: http://%s:8080/download/%s\n", localIP, p)
		}
	} else {
		fmt.Println("- No download files configured (use -f for single file or -x for multiple files)")
	}

	// Execute QR code generation logic asynchronously in a goroutine to avoid blocking HTTP server startup
	go func() {
		config := qrterminal.Config{
			Level:          qrterminal.M,
			Writer:         os.Stdout,
			HalfBlocks:     true,
			BlackChar:      BLACK_BLACK,
			WhiteBlackChar: WHITE_BLACK,
			WhiteChar:      WHITE_WHITE,
			BlackWhiteChar: BLACK_WHITE,
			QuietZone:      1,
		}

		var qrURL string
		if allowSingleFilePath != "" {
			fmt.Printf("\n📱️Scan below qrcode to download file: %s\n", allowSingleFilePath)
			qrURL = "http://" + localIP + ":8080/download/" + allowSingleFilePath
		} else if len(allowMultiFilePaths) > 0 {
			fmt.Printf("\n📱️Scan below qrcode to access downloadable files list.\n")
			qrURL = "http://" + localIP + ":8080/downloads"
		} else {
			fmt.Printf("\n📱️Scan below qrcode to upload files.\n")
			qrURL = "http://" + localIP + ":8080"
		}
		qrterminal.GenerateWithConfig(qrURL, config)
	}()

	// Start HTTP server
	err = http.ListenAndServe(":8080", nil)
	if err != nil {
		fmt.Printf("Failed to start server: %v\n", err)
	}
}
