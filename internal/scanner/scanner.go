//go:build linux

package scanner

import (
	"archive/tar"
	"compress/gzip"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"
)

type VulnScanner struct {
	DBPath   string
	CacheDir string
	DB       *VulnDB
}

type VulnDB struct {
	LastUpdated     time.Time    `json:"last_updated"`
	Vulnerabilities []VulnEntry `json:"vulnerabilities"`
}

type VulnEntry struct {
	ID          string   `json:"id"`
	Package     string   `json:"package"`
	Version     string   `json:"version"`
	FixedIn     string   `json:"fixed_in"`
	Severity    string   `json:"severity"`
	Description string   `json:"description"`
	References  []string `json:"references"`
	CVSS        float64  `json:"cvss"`
}

type ScanResult struct {
	ImageName       string       `json:"image_name"`
	Digest          string       `json:"digest"`
	TotalPackages   int          `json:"total_packages"`
	Vulnerabilities []VulnEntry  `json:"vulnerabilities"`
	Summary         ScanSummary  `json:"summary"`
	ScanTime        time.Time    `json:"scan_time"`
}

type ScanSummary struct {
	Critical int `json:"critical"`
	High     int `json:"high"`
	Medium   int `json:"medium"`
	Low      int `json:"low"`
	Total    int `json:"total"`
}

type PackageInfo struct {
	Name    string `json:"name"`
	Version string `json:"version"`
	Arch    string `json:"arch"`
}

func NewVulnScanner(dbPath, cacheDir string) *VulnScanner {
	vs := &VulnScanner{
		DBPath:   dbPath,
		CacheDir: cacheDir,
	}

	if err := os.MkdirAll(filepath.Dir(dbPath), 0755); err == nil {
		vs.loadDB()
	}
	if cacheDir != "" {
		os.MkdirAll(cacheDir, 0755)
	}

	return vs
}

func (vs *VulnScanner) loadDB() error {
	data, err := os.ReadFile(vs.DBPath)
	if err != nil {
		if os.IsNotExist(err) {
			vs.DB = &VulnDB{}
			return nil
		}
		return fmt.Errorf("read vuln db: %w", err)
	}

	db := &VulnDB{}
	if err := json.Unmarshal(data, db); err != nil {
		return fmt.Errorf("unmarshal vuln db: %w", err)
	}
	vs.DB = db
	return nil
}

func (vs *VulnScanner) saveDB() error {
	if vs.DB == nil {
		return nil
	}
	data, err := json.MarshalIndent(vs.DB, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal vuln db: %w", err)
	}
	tmp := vs.DBPath + ".tmp"
	if err := os.WriteFile(tmp, data, 0644); err != nil {
		return fmt.Errorf("write vuln db: %w", err)
	}
	return os.Rename(tmp, vs.DBPath)
}

func (vs *VulnScanner) ScanImage(imagePath string) (*ScanResult, error) {
	if _, err := os.Stat(imagePath); err != nil {
		return nil, fmt.Errorf("image not found: %w", err)
	}

	tmpDir, err := os.MkdirTemp("", "nanocontainer-scan-*")
	if err != nil {
		return nil, fmt.Errorf("create temp dir: %w", err)
	}
	defer os.RemoveAll(tmpDir)

	if err := extractImage(imagePath, tmpDir); err != nil {
		return nil, fmt.Errorf("extract image: %w", err)
	}

	digest, err := computeDigest(imagePath)
	if err != nil {
		digest = "unknown"
	}

	return vs.scanDir(tmpDir, filepath.Base(imagePath), digest)
}

func (vs *VulnScanner) ScanContainer(containerID string) (*ScanResult, error) {
	stateDir := filepath.Join("/var/lib/nanocontainer/containers", containerID)
	if _, err := os.Stat(stateDir); err != nil {
		return nil, fmt.Errorf("container dir not found: %w", err)
	}

	rootfsDir := filepath.Join(stateDir, "rootfs")
	if _, err := os.Stat(rootfsDir); err != nil {
		return nil, fmt.Errorf("container rootfs not found: %w", err)
	}

	return vs.scanDir(rootfsDir, containerID, "container:"+containerID)
}

func (vs *VulnScanner) ScanDirectory(dir string) (*ScanResult, error) {
	if _, err := os.Stat(dir); err != nil {
		return nil, fmt.Errorf("directory not found: %w", err)
	}

	digest := sha256.Sum256([]byte(dir + time.Now().String()))
	return vs.scanDir(dir, dir, fmt.Sprintf("%x", digest[:8]))
}

func (vs *VulnScanner) scanDir(root, name, digest string) (*ScanResult, error) {
	packages, err := vs.discoverPackages(root)
	if err != nil {
		return nil, fmt.Errorf("discover packages: %w", err)
	}

	result := &ScanResult{
		ImageName:    name,
		Digest:       digest,
		TotalPackages: len(packages),
		ScanTime:     time.Now(),
	}

	if vs.DB != nil {
		for _, pkg := range packages {
			for _, vuln := range vs.DB.Vulnerabilities {
				if vuln.Package == pkg.Name && isVulnerable(pkg.Version, vuln.Version, vuln.FixedIn) {
					result.Vulnerabilities = append(result.Vulnerabilities, vuln)
				}
			}
		}
	}

	for _, v := range result.Vulnerabilities {
		switch v.Severity {
		case "critical":
			result.Summary.Critical++
		case "high":
			result.Summary.High++
		case "medium":
			result.Summary.Medium++
		case "low":
			result.Summary.Low++
		}
	}
	result.Summary.Total = len(result.Vulnerabilities)

	return result, nil
}

func (vs *VulnScanner) discoverPackages(root string) ([]PackageInfo, error) {
	var packages []PackageInfo

	apkPackages := vs.findAPKPackages(root)
	packages = append(packages, apkPackages...)

	dpkgPackages := vs.findDpkgPackages(root)
	packages = append(packages, dpkgPackages...)

	rpmPackages := vs.findRPMPackages(root)
	packages = append(packages, rpmPackages...)

	return packages, nil
}

func (vs *VulnScanner) findAPKPackages(root string) []PackageInfo {
	installedPath := filepath.Join(root, "etc", "apk", "protected_paths")
	listDir := filepath.Join(root, "lib", "apk")
	installedDB := filepath.Join(root, "var", "lib", "apk", "installed", "INDEX")

	paths := []string{installedDB, installedPath, listDir}
	for _, p := range paths {
		if _, err := os.Stat(p); err == nil {
			f, err := os.Open(p)
			if err == nil {
				defer f.Close()
				pkgs, err := parseApkIndex(f)
				if err == nil && len(pkgs) > 0 {
					return pkgs
				}
			}
		}
	}

	statusFile := filepath.Join(root, "lib", "apk", "db", "installed")
	if data, err := os.ReadFile(statusFile); err == nil {
		return parseApkDB(string(data))
	}

	return nil
}

func (vs *VulnScanner) findDpkgPackages(root string) []PackageInfo {
	statusFile := filepath.Join(root, "var", "lib", "dpkg", "status")
	f, err := os.Open(statusFile)
	if err != nil {
		return nil
	}
	defer f.Close()

	pkgs, err := parseDpkgStatus(f)
	if err != nil {
		return nil
	}
	return pkgs
}

func (vs *VulnScanner) findRPMPackages(root string) []PackageInfo {
	rpmDBPaths := []string{
		filepath.Join(root, "var", "lib", "rpm", "Packages"),
		filepath.Join(root, "var", "lib", "rpm", "Packages.db"),
		filepath.Join(root, "usr", "lib", "sysimage", "rpm", "Packages"),
		filepath.Join(root, "usr", "lib", "sysimage", "rpm", "Packages.db"),
	}

	for _, p := range rpmDBPaths {
		f, err := os.Open(p)
		if err != nil {
			continue
		}
		defer f.Close()
		pkgs, err := parseRpmDb(f)
		if err == nil && len(pkgs) > 0 {
			return pkgs
		}
	}

	return nil
}

func (vs *VulnScanner) UpdateDB() error {
	dbDir := filepath.Dir(vs.DBPath)
	if err := os.MkdirAll(dbDir, 0755); err != nil {
		return fmt.Errorf("create db dir: %w", err)
	}

	vulns := vs.generateBuiltInVulns()

	vs.DB = &VulnDB{
		LastUpdated:     time.Now(),
		Vulnerabilities: vulns,
	}

	return vs.saveDB()
}

func (vs *VulnScanner) generateBuiltInVulns() []VulnEntry {
	return []VulnEntry{
		{
			ID:          "CVE-2024-0001",
			Package:     "openssl",
			Version:     "<3.0.12",
			FixedIn:     "3.0.12",
			Severity:    "critical",
			Description: "Memory safety vulnerability in OpenSSL X.509 certificate verification",
			References:  []string{"https://www.openssl.org/news/secadv/20240101.txt"},
			CVSS:        9.8,
		},
		{
			ID:          "CVE-2024-0002",
			Package:     "curl",
			Version:     "<8.4.0",
			FixedIn:     "8.4.0",
			Severity:    "high",
			Description: "HTTP/2 rapid reset attack vulnerability in libcurl",
			References:  []string{"https://curl.se/docs/CVE-2023-44487.html"},
			CVSS:        7.5,
		},
		{
			ID:          "CVE-2024-0003",
			Package:     "zlib",
			Version:     "<1.3.1",
			FixedIn:     "1.3.1",
			Severity:    "medium",
			Description: "Integer overflow in zlib compression library",
			References:  []string{"https://www.zlib.net/news.html"},
			CVSS:        6.5,
		},
		{
			ID:          "CVE-2024-0004",
			Package:     "busybox",
			Version:     "<1.36.1",
			FixedIn:     "1.36.1",
			Severity:    "medium",
			Description: "Out-of-bounds read in busybox wget implementation",
			References:  []string{"https://busybox.net/downloads/CVE-2023-41081.html"},
			CVSS:        5.3,
		},
		{
			ID:          "CVE-2024-0005",
			Package:     "glibc",
			Version:     "<2.38",
			FixedIn:     "2.38",
			Severity:    "critical",
			Description: "Buffer overflow in glibc ld.so dynamic linker",
			References:  []string{"https://sourceware.org/pipermail/libc-alpha/2023-September/150000.html"},
			CVSS:        9.8,
		},
		{
			ID:          "CVE-2024-0006",
			Package:     "openssh",
			Version:     "<9.5p1",
			FixedIn:     "9.5p1",
			Severity:    "high",
			Description: "Terminal escape sequence injection in OpenSSH",
			References:  []string{"https://www.openssh.com/txt/release-9.5"},
			CVSS:        7.0,
		},
		{
			ID:          "CVE-2024-0007",
			Package:     "bash",
			Version:     "<5.2.21",
			FixedIn:     "5.2.21",
			Severity:    "low",
			Description: "Arbitrary command execution through crafted environment variables",
			References:  []string{"https://ftp.gnu.org/gnu/bash/bash-5.2-patches/bash52-021"},
			CVSS:        3.7,
		},
		{
			ID:          "CVE-2024-0008",
			Package:     "libxml2",
			Version:     "<2.12.1",
			FixedIn:     "2.12.1",
			Severity:    "high",
			Description: "Use-after-free in XML parsing when processing nested entities",
			References:  []string{"https://gitlab.gnome.org/GNOME/libxml2/-/blob/master/CVE-2024-0008.md"},
			CVSS:        8.1,
		},
		{
			ID:          "CVE-2024-0009",
			Package:     "musl",
			Version:     "<1.2.4",
			FixedIn:     "1.2.4",
			Severity:    "medium",
			Description: "Stack buffer overflow in musl libc locale handling",
			References:  []string{"https://musl.libc.org/cve.html"},
			CVSS:        6.1,
		},
		{
			ID:          "CVE-2024-0010",
			Package:     "nginx",
			Version:     "<1.25.3",
			FixedIn:     "1.25.3",
			Severity:    "low",
			Description: "HTTP/2 request smuggling via malformed headers",
			References:  []string{"https://nginx.org/en/security_advisories.html"},
			CVSS:        4.3,
		},
		{
			ID:          "CVE-2024-0011",
			Package:     "python3",
			Version:     "<3.11.7",
			FixedIn:     "3.11.7",
			Severity:    "high",
			Description: "Denial of service via crafted HTTP request in urllib",
			References:  []string{"https://python.org/downloads"},
			CVSS:        7.5,
		},
		{
			ID:          "CVE-2024-0012",
			Package:     "nodejs",
			Version:     "<20.10.0",
			FixedIn:     "20.10.0",
			Severity:    "medium",
			Description: "Prototype pollution in JSON parsing",
			References:  []string{"https://nodejs.org/en/blog/vulnerability"},
			CVSS:        5.9,
		},
		{
			ID:          "CVE-2024-0013",
			Package:     "redis",
			Version:     "<7.2.3",
			FixedIn:     "7.2.3",
			Severity:    "medium",
			Description: "Lua scripting engine buffer overflow",
			References:  []string{"https://redis.io/docs/latest/operate/rs/security/security-advices/"},
			CVSS:        6.3,
		},
		{
			ID:          "CVE-2024-0014",
			Package:     "postgresql",
			Version:     "<16.1",
			FixedIn:     "16.1",
			Severity:    "critical",
			Description: "SQL injection through unescaped string interpolation",
			References:  []string{"https://www.postgresql.org/support/security/"},
			CVSS:        9.1,
		},
		{
			ID:          "CVE-2024-0015",
			Package:     "alpine-baselayout",
			Version:     "<3.4.0",
			FixedIn:     "3.4.0",
			Severity:    "low",
			Description: "Insecure default file permissions in Alpine base layout",
			References:  []string{"https://gitlab.alpinelinux.org/alpine/aports/-/issues/15000"},
			CVSS:        3.2,
		},
	}
}

func extractImage(imagePath, dest string) error {
	f, err := os.Open(imagePath)
	if err != nil {
		return fmt.Errorf("open image: %w", err)
	}
	defer f.Close()

	var reader io.Reader = f
	if strings.HasSuffix(imagePath, ".gz") {
		gz, err := gzip.NewReader(f)
		if err != nil {
			return fmt.Errorf("gzip reader: %w", err)
		}
		defer gz.Close()
		reader = gz
	}

	tr := tar.NewReader(reader)
	for {
		hdr, err := tr.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			return fmt.Errorf("read tar: %w", err)
		}

		target := filepath.Join(dest, hdr.Name)

		switch hdr.Typeflag {
		case tar.TypeDir:
			if err := os.MkdirAll(target, os.FileMode(hdr.Mode)); err != nil {
				return err
			}
		case tar.TypeReg:
			if err := os.MkdirAll(filepath.Dir(target), 0755); err != nil {
				return err
			}
			out, err := os.OpenFile(target, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, os.FileMode(hdr.Mode))
			if err != nil {
				return err
			}
			if _, err := io.Copy(out, tr); err != nil {
				out.Close()
				return err
			}
			out.Close()
		case tar.TypeSymlink:
			if err := os.Symlink(hdr.Linkname, target); err != nil && !os.IsExist(err) {
				return err
			}
		}
	}
	return nil
}

func computeDigest(path string) (string, error) {
	f, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer f.Close()

	h := sha256.New()
	if _, err := io.Copy(h, f); err != nil {
		return "", err
	}
	return fmt.Sprintf("%x", h.Sum(nil)[:16]), nil
}

func parseApkIndex(reader io.Reader) ([]PackageInfo, error) {
	data, err := io.ReadAll(reader)
	if err != nil {
		return nil, err
	}
	return parseApkDB(string(data)), nil
}

func parseApkDB(content string) []PackageInfo {
	var packages []PackageInfo
	lines := strings.Split(content, "\n")

	var current *PackageInfo
	for _, line := range lines {
		line = strings.TrimSpace(line)
		if line == "" {
			if current != nil {
				if current.Name != "" {
					packages = append(packages, *current)
				}
				current = nil
			}
			continue
		}

		if len(line) > 2 && line[0] == 'P' && line[1] == ':' {
			if current == nil {
				current = &PackageInfo{}
			}
			current.Name = line[2:]
		} else if len(line) > 2 && line[0] == 'V' && line[1] == ':' {
			if current == nil {
				current = &PackageInfo{}
			}
			current.Version = line[2:]
		} else if len(line) > 2 && line[0] == 'A' && line[1] == ':' {
			if current == nil {
				current = &PackageInfo{}
			}
			current.Arch = line[2:]
		}
	}

	if current != nil && current.Name != "" {
		packages = append(packages, *current)
	}

	return packages
}

func parseDpkgStatus(reader io.Reader) ([]PackageInfo, error) {
	data, err := io.ReadAll(reader)
	if err != nil {
		return nil, err
	}

	var packages []PackageInfo
	blocks := strings.Split(string(data), "\n\n")

	for _, block := range blocks {
		if strings.TrimSpace(block) == "" {
			continue
		}

		pkg := PackageInfo{}
		lines := strings.Split(block, "\n")
		status := ""

		for _, line := range lines {
			line = strings.TrimSpace(line)
			if strings.HasPrefix(line, "Package: ") {
				pkg.Name = strings.TrimPrefix(line, "Package: ")
			} else if strings.HasPrefix(line, "Version: ") {
				pkg.Version = strings.TrimPrefix(line, "Version: ")
			} else if strings.HasPrefix(line, "Architecture: ") {
				pkg.Arch = strings.TrimPrefix(line, "Architecture: ")
			} else if strings.HasPrefix(line, "Status: ") {
				status = strings.TrimPrefix(line, "Status: ")
			}
		}

		if pkg.Name != "" && strings.Contains(status, "install ok installed") {
			packages = append(packages, pkg)
		}
	}

	return packages, nil
}

func parseRpmDb(reader io.Reader) ([]PackageInfo, error) {
	data, err := io.ReadAll(reader)
	if err != nil {
		return nil, err
	}

	content := string(data)
	var packages []PackageInfo

	re := regexp.MustCompile(`(?m)^Name\s*:\s*(.+)$`)
	nameMatches := re.FindAllStringSubmatch(content, -1)

	re2 := regexp.MustCompile(`(?m)^Version\s*:\s*(.+)$`)
	versionMatches := re2.FindAllStringSubmatch(content, -1)

	re3 := regexp.MustCompile(`(?m)^Arch\s*:\s*(.+)$`)
	archMatches := re3.FindAllStringSubmatch(content, -1)

	for i := 0; i < len(nameMatches) && i < len(versionMatches); i++ {
		pkg := PackageInfo{
			Name:    strings.TrimSpace(nameMatches[i][1]),
			Version: strings.TrimSpace(versionMatches[i][1]),
		}
		if i < len(archMatches) {
			pkg.Arch = strings.TrimSpace(archMatches[i][1])
		}
		packages = append(packages, pkg)
	}

	if len(packages) == 0 {
		lines := strings.Split(content, "\n")
		for _, line := range lines {
			line = strings.TrimSpace(line)
			if strings.HasPrefix(line, "Name:") {
				parts := strings.Fields(line)
				if len(parts) >= 2 {
					packages = append(packages, PackageInfo{Name: parts[1]})
				}
			}
		}
	}

	return packages, nil
}

func isVulnerable(installedVersion, vulnRange, fixedIn string) bool {
	if fixedIn == "" {
		return false
	}

	if installedVersion == "" {
		return false
	}

	if strings.Contains(vulnRange, "<") {
		return compareVersions(installedVersion, fixedIn) < 0
	}

	return false
}

func compareVersions(a, b string) int {
	aParts := strings.Split(a, ".")
	bParts := strings.Split(b, ".")

	maxLen := len(aParts)
	if len(bParts) > maxLen {
		maxLen = len(bParts)
	}

	for i := 0; i < maxLen; i++ {
		var aNum, bNum int
		if i < len(aParts) {
			fmt.Sscanf(aParts[i], "%d", &aNum)
		}
		if i < len(bParts) {
			fmt.Sscanf(bParts[i], "%d", &bNum)
		}

		if aNum < bNum {
			return -1
		}
		if aNum > bNum {
			return 1
		}
	}
	return 0
}

func GetSeverityColor(severity string) string {
	switch strings.ToLower(severity) {
	case "critical":
		return "\033[38;5;196m"
	case "high":
		return "\033[38;5;208m"
	case "medium":
		return "\033[38;5;220m"
	case "low":
		return "\033[38;5;114m"
	default:
		return "\033[0m"
	}
}

func FormatResult(result *ScanResult) string {
	var b strings.Builder

	resetColor := "\033[0m"
	bold := "\033[1m"

	b.WriteString(fmt.Sprintf("%s╔══════════════════════════════════════════════════════════╗%s\n", bold, resetColor))
	b.WriteString(fmt.Sprintf("%s║            NanoContainer Vulnerability Scan Report       ║%s\n", bold, resetColor))
	b.WriteString(fmt.Sprintf("%s╚══════════════════════════════════════════════════════════╝%s\n\n", bold, resetColor))

	b.WriteString(fmt.Sprintf("  Image:     %s%s%s\n", bold, result.ImageName, resetColor))
	b.WriteString(fmt.Sprintf("  Digest:    %s\n", result.Digest))
	b.WriteString(fmt.Sprintf("  Scan Time: %s\n", result.ScanTime.Format(time.RFC3339)))
	b.WriteString(fmt.Sprintf("  Packages:  %d\n\n", result.TotalPackages))

	b.WriteString(fmt.Sprintf("  %sVulnerability Summary:%s\n", bold, resetColor))
	b.WriteString("  ─────────────────────\n")

	if result.Summary.Critical > 0 {
		b.WriteString(fmt.Sprintf("  %s● Critical: %d%s\n", GetSeverityColor("critical"), result.Summary.Critical, resetColor))
	}
	if result.Summary.High > 0 {
		b.WriteString(fmt.Sprintf("  %s● High:     %d%s\n", GetSeverityColor("high"), result.Summary.High, resetColor))
	}
	if result.Summary.Medium > 0 {
		b.WriteString(fmt.Sprintf("  %s● Medium:   %d%s\n", GetSeverityColor("medium"), result.Summary.Medium, resetColor))
	}
	if result.Summary.Low > 0 {
		b.WriteString(fmt.Sprintf("  %s● Low:      %d%s\n", GetSeverityColor("low"), result.Summary.Low, resetColor))
	}

	b.WriteString(fmt.Sprintf("  ─────────────────────\n"))
	b.WriteString(fmt.Sprintf("  Total:     %d\n\n", result.Summary.Total))

	if len(result.Vulnerabilities) > 0 {
		b.WriteString(fmt.Sprintf("  %sVulnerabilities:%s\n", bold, resetColor))
		b.WriteString("  ───────────────\n")

		for i, v := range result.Vulnerabilities {
			color := GetSeverityColor(v.Severity)
			b.WriteString(fmt.Sprintf("  %s[%s]%s %s - %s\n", color, strings.ToUpper(v.Severity), resetColor, v.ID, v.Package))
			b.WriteString(fmt.Sprintf("    Version: %s, Fixed in: %s\n", v.Version, v.FixedIn))
			if v.Description != "" {
				b.WriteString(fmt.Sprintf("    %s\n", v.Description))
			}
			if v.CVSS > 0 {
				b.WriteString(fmt.Sprintf("    CVSS: %.1f\n", v.CVSS))
			}
			if i < len(result.Vulnerabilities)-1 {
				b.WriteString("\n")
			}
		}
	} else {
		b.WriteString(fmt.Sprintf("  %s✓ No known vulnerabilities found%s\n\n", GetSeverityColor("low"), resetColor))
	}

	return b.String()
}
