////////////////////////////////////////////////////////////////////////////////
// Script Name: user/add.go
// Description: Create a new user with the provided plan_name.
// Usage: opencli user-add <USERNAME> <PASSWORD|generate> <EMAIL> "<PLAN_NAME>"
//        [--send-email] [--debug] [--skip-images]
//        [--reseller=<RESELLER_USERNAME>]
//        [--server=<IP_ADDRESS>] [--key=<SSH_KEY_PATH>]
//        [--webserver=<nginx|apache|openresty|openlitespeed|litespeed|varnish+nginx|...>]
//        [--sql=<mysql|mariadb>]
// Docs: https://docs.openpanel.com
// Author: Stefan Pejcic
// Created: 01.10.2023
// Last Modified: 25.02.2026
// Company: openpanel.com
// Copyright (c) openpanel.com
// 
// Permission is hereby granted, free of charge, to any person obtaining a copy
// of this software and associated documentation files (the "Software"), to deal
// in the Software without restriction, including without limitation the rights
// to use, copy, modify, merge, publish, distribute, sublicense, and/or sell
// copies of the Software, and to permit persons to whom the Software is
// furnished to do so, subject to the following conditions:
// 
// The above copyright notice and this permission notice shall be included in
// all copies or substantial portions of the Software.
// 
// THE SOFTWARE IS PROVIDED "AS IS", WITHOUT WARRANTY OF ANY KIND, EXPRESS OR
// IMPLIED, INCLUDING BUT NOT LIMITED TO THE WARRANTIES OF MERCHANTABILITY,
// FITNESS FOR A PARTICULAR PURPOSE AND NONINFRINGEMENT. IN NO EVENT SHALL THE
// AUTHORS OR COPYRIGHT HOLDERS BE LIABLE FOR ANY CLAIM, DAMAGES OR OTHER
// LIABILITY, WHETHER IN AN ACTION OF CONTRACT, TORT OR OTHERWISE, ARISING FROM,
// OUT OF OR IN CONNECTION WITH THE SOFTWARE OR THE USE OR OTHER DEALINGS IN
// THE SOFTWARE.
////////////////////////////////////////////////////////////////////////////////

package main

import (
	"bufio"
	"crypto/rand"
	"encoding/base64"
	"fmt"
	"math/big"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"syscall"
	"time"
)

// ─────────────────────────────────────────────────────────────
// Constants
// ─────────────────────────────────────────────────────────────

const (
	forbiddenUsernamesFile = "/etc/openpanel/openadmin/config/forbidden_usernames.txt"
	dbConfigFile           = "/usr/local/opencli/db.sh"
	panelConfigFile        = "/etc/openpanel/openpanel/conf/openpanel.config"
	adminConfigFile        = "/etc/openpanel/openadmin/config/admin.ini"
	adminDBFile            = "/etc/openpanel/openadmin/users.db"
	lockFile               = "/var/lock/openpanel_user_add.lock"
)

// ─────────────────────────────────────────────────────────────
// Config / state
// ─────────────────────────────────────────────────────────────

type Config struct {
	Username      string
	Password      string
	Email         string
	PlanName      string
	Debug         bool
	SkipImagePull bool
	SendEmail     bool
	Reseller      string
	Server        string // IP of slave node
	Key           string // SSH key path
	KeyFlag       string // composed "-i key -o StrictHostKeyChecking=no ..."
	Webserver     string
	SQLType       string
	// resolved at runtime
	NodeIPAddress    string
	Hostname         string
	ContextFlag      string
	UserID           string
	PlanID           string
	CPU              string
	RAM              string
	DiskLimit        int
	Inodes           int
	Bandwidth        string
	HashedPassword   string
	KeyValue         string // enterprise key
	MaxAccounts      string
	CurrentAccounts  int
	DefaultPHPVersion string
	// port assignments
	Ports [7]string // port_1 … port_7
}

// ─────────────────────────────────────────────────────────────
// main
// ─────────────────────────────────────────────────────────────

func main() {
	args := os.Args[1:]
	if len(args) < 4 || len(args) > 11 {
		usage()
		os.Exit(1)
	}

	cfg := &Config{
		Username: strings.ToLower(args[0]),
		Password: args[1],
		Email:    args[2],
		PlanName: args[3],
	}

	cfg.checkEnterprise()
	cfg.parseFlags(args[4:])
	cfg.setupLock()

	// ordered steps (mirrors the MAIN section in bash)
	cfg.getHostnameOfMaster()
	cfg.checkUsernameIsValid()
	cfg.validatePasswordInLists(cfg.Password)
	cfg.checkIfDefaultSlaveServerIsSet() // may set cfg.Server/Key before parseFlags — already done above, but we re-check defaults
	cfg.getSlaveIfSet()
	cfg.getExistingUsersCount()
	cfg.getPlanInfoAndCheckRequirements()
	cfg.checkIfReseller()
	cfg.printDebugInfoBeforeStartingCreation()
	cfg.validateSSHLogin()
	cfg.createUserSetQuotaAndPassword()
	cfg.sshfsMounts()
	cfg.setupSSHKey()
	cfg.installDockerAndAddUser()
	cfg.createVolume()
	cfg.dockerRootless()
	cfg.dockerCompose()
	cfg.createContext()
	cfg.testComposeCommandForUser()
	cfg.getPHPVersion()
	cfg.runDocker()
	cfg.reloadUserQuotas()
	cfg.generateUserPasswordHash()
	cfg.copySkeletonFiles()
	cfg.downloadImages()
	cfg.startPanelService()
	cfg.saveUserToDB()
	cfg.updateAccountsForReseller()
	cfg.collectStats()
	cfg.sendEmailToNewUser()
	cfg.permissionsDo()
	os.Exit(0)
}

// ─────────────────────────────────────────────────────────────
// Usage
// ─────────────────────────────────────────────────────────────

func usage() {
	fmt.Println("Usage: opencli user-add <username> <password|generate> <email> '<plan_name>' [--send-email] [--debug] [--reseller=<RESELLER_USER>] [--server=<IP_ADDRESS>] [--key=<KEY_PATH>]")
	fmt.Println()
	fmt.Println("Required arguments:")
	fmt.Printf("  %-26s %s\n", "<username>", "The username of the new user.")
	fmt.Printf("  %-26s %s\n", "<password|generate>", "The password for the new user, or 'generate' to auto-generate a password.")
	fmt.Printf("  %-26s %s\n", "<email>", "The email address associated with the new user.")
	fmt.Printf("  %-26s %s\n", "<plan_name>", "The plan to assign to the new user.")
	fmt.Println()
	fmt.Println("Optional flags:")
	fmt.Printf("  %-26s %s\n", "--send-email", "Send a welcome email to the user.")
	fmt.Printf("  %-26s %s\n", "--debug", "Enable debug mode for additional output.")
}

// ─────────────────────────────────────────────────────────────
// Helpers
// ─────────────────────────────────────────────────────────────

func (c *Config) log(msg string) {
	if c.Debug {
		fmt.Println(msg)
	}
}

func runCmd(name string, args ...string) (string, error) {
	cmd := exec.Command(name, args...)
	out, err := cmd.CombinedOutput()
	return strings.TrimSpace(string(out)), err
}

func runCmdSilent(name string, args ...string) error {
	cmd := exec.Command(name, args...)
	cmd.Stdout = nil
	cmd.Stderr = nil
	return cmd.Run()
}

func sshRun(keyFlag, userAtHost, command string) (string, error) {
	sshArgs := []string{}
	if keyFlag != "" {
		sshArgs = append(sshArgs, strings.Fields(keyFlag)...)
	}
	sshArgs = append(sshArgs, userAtHost, command)
	return runCmd("ssh", sshArgs...)
}

func sshRunSilent(keyFlag, userAtHost, command string) error {
	_, err := sshRun(keyFlag, userAtHost, command)
	return err
}

func fileExists(path string) bool {
	_, err := os.Stat(path)
	return err == nil
}

func readConfigValue(file, key string) string {
	f, err := os.Open(file)
	if err != nil {
		return ""
	}
	defer f.Close()
	scanner := bufio.NewScanner(f)
	prefix := key + "="
	for scanner.Scan() {
		line := scanner.Text()
		if strings.HasPrefix(line, prefix) {
			val := strings.TrimPrefix(line, prefix)
			val = strings.TrimSpace(val)
			return val
		}
	}
	return ""
}

func readConfigValueINI(file, key string) string {
	f, err := os.Open(file)
	if err != nil {
		return ""
	}
	defer f.Close()
	re := regexp.MustCompile(`(?i)^` + regexp.QuoteMeta(key) + `\s*=\s*(.*)`)
	scanner := bufio.NewScanner(f)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		m := re.FindStringSubmatch(line)
		if m != nil {
			return strings.TrimSpace(m[1])
		}
	}
	return ""
}

func generatePassword() string {
	const chars = "abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789"
	result := make([]byte, 12)
	for i := range result {
		n, _ := rand.Int(rand.Reader, big.NewInt(int64(len(chars))))
		result[i] = chars[n.Int64()]
	}
	return string(result)
}

func randomBase64(n int) string {
	b := make([]byte, n)
	rand.Read(b)
	s := base64.StdEncoding.EncodeToString(b)
	// keep only alphanumeric
	re := regexp.MustCompile(`[^a-zA-Z0-9]`)
	return re.ReplaceAllString(s, "")[:n]
}

func isValidEmail(email string) bool {
	re := regexp.MustCompile(`^[a-zA-Z0-9._%+\-]+@[a-zA-Z0-9.\-]+\.[a-zA-Z]{2,}$`)
	return re.MatchString(email)
}

func isValidIPv4(ip string) bool {
	parsed := net.ParseIP(ip)
	return parsed != nil && strings.Contains(ip, ".")
}

// ─────────────────────────────────────────────────────────────
// Flag parsing
// ─────────────────────────────────────────────────────────────

func (c *Config) parseFlags(args []string) {
	for _, arg := range args {
		switch {
		case arg == "--debug":
			c.Debug = true
		case arg == "--send-email":
			c.SendEmail = true
		case arg == "--skip-images":
			c.SkipImagePull = true
		case strings.HasPrefix(arg, "--reseller="):
			c.Reseller = strings.TrimPrefix(arg, "--reseller=")
		case strings.HasPrefix(arg, "--server="):
			c.Server = strings.TrimPrefix(arg, "--server=")
		case strings.HasPrefix(arg, "--key="):
			c.Key = strings.TrimPrefix(arg, "--key=")
			c.KeyFlag = fmt.Sprintf("-i %s -o StrictHostKeyChecking=no -o UserKnownHostsFile=/dev/null -o BatchMode=yes", c.Key)
		case strings.HasPrefix(arg, "--sql="):
			c.SQLType = strings.TrimPrefix(arg, "--sql=")
		case strings.HasPrefix(arg, "--webserver="):
			ws := strings.TrimPrefix(arg, "--webserver=")
			ws = strings.Trim(ws, `"`)
			ws = strings.TrimSpace(ws)
			c.Webserver = ws
		}
	}
}

// ─────────────────────────────────────────────────────────────
// Enterprise / key check
// ─────────────────────────────────────────────────────────────

func (c *Config) checkEnterprise() {
	c.KeyValue = readConfigValue(panelConfigFile, "key")
}

// ─────────────────────────────────────────────────────────────
// Lock
// ─────────────────────────────────────────────────────────────

func (c *Config) setupLock() {
	lf, err := os.OpenFile(lockFile, os.O_CREATE|os.O_RDWR, 0600)
	if err != nil {
		fmt.Fprintf(os.Stderr, "[✘] Error: Cannot open lock file: %v\n", err)
		os.Exit(1)
	}
	err = syscall.Flock(int(lf.Fd()), syscall.LOCK_EX|syscall.LOCK_NB)
	if err != nil {
		fmt.Println("[✘] Error: A user creation process is already running.")
		fmt.Println("Please wait for it to complete before starting a new one. Exiting.")
		os.Exit(1)
	}
	// Release lock on exit via deferred close (lf stays open for the life of the process)
}

// ─────────────────────────────────────────────────────────────
// Cleanup helpers
// ─────────────────────────────────────────────────────────────

func (c *Config) cleanup() {
	os.Remove(lockFile)
}

func (c *Config) hardCleanup() {
	runCmdSilent("killall", "-u", c.Username, "-9")
	runCmdSilent("deluser", "--remove-home", c.Username)
	os.RemoveAll(fmt.Sprintf("/etc/openpanel/openpanel/core/users/%s", c.Username))
	runCmdSilent("docker", "context", "rm", c.Username)
	runCmdSilent("quotacheck", "-avm")
	os.MkdirAll("/etc/openpanel/openpanel/core/users/", 0755)
	runCmd("bash", "-c", "repquota -u / > /etc/openpanel/openpanel/core/users/repquota")
	os.Exit(1)
}

// ─────────────────────────────────────────────────────────────
// Hostname
// ─────────────────────────────────────────────────────────────

func (c *Config) getHostnameOfMaster() {
	h, err := os.Hostname()
	if err == nil {
		c.Hostname = h
	}
}

// ─────────────────────────────────────────────────────────────
// Default slave server from admin.ini
// ─────────────────────────────────────────────────────────────

func (c *Config) checkIfDefaultSlaveServerIsSet() {
	defaultNode := readConfigValueINI(adminConfigFile, "default_node")
	if defaultNode == "" {
		return
	}
	defaultKeyPath := readConfigValueINI(adminConfigFile, "default_ssh_key_path")
	if defaultKeyPath != "" && c.Server == "" {
		c.Server = defaultNode
		c.Key = defaultKeyPath
		fmt.Printf("Using default node %s and ssh key path\n", c.Server)
	}
}

// ─────────────────────────────────────────────────────────────
// Validate username
// ─────────────────────────────────────────────────────────────

func (c *Config) checkUsernameIsValid() {
	u := c.Username

	if strings.ContainsAny(u, " \t") {
		fmt.Println("[✘] Error: The username cannot contain spaces.")
		fmt.Println("       docs: https://openpanel.com/docs/articles/accounts/forbidden-usernames/#openpanel")
		os.Exit(1)
	}
	if strings.ContainsAny(u, "-_") {
		fmt.Println("[✘] Error: The username cannot contain hyphens or underscores.")
		fmt.Println("       docs: https://openpanel.com/docs/articles/accounts/forbidden-usernames/#openpanel")
		os.Exit(1)
	}
	if !regexp.MustCompile(`^[a-zA-Z0-9]+$`).MatchString(u) {
		fmt.Println("[✘] Error: The username can only contain letters and numbers.")
		fmt.Println("       docs: https://openpanel.com/docs/articles/accounts/forbidden-usernames/#openpanel")
		os.Exit(1)
	}
	if regexp.MustCompile(`^[0-9]+$`).MatchString(u) {
		fmt.Println("[✘] Error: The username cannot consist entirely of numbers.")
		fmt.Println("       docs: https://openpanel.com/docs/articles/accounts/forbidden-usernames/#openpanel")
		os.Exit(1)
	}
	if len(u) < 3 {
		fmt.Println("[✘] Error: The username must be at least 3 characters long.")
		fmt.Println("       docs: https://openpanel.com/docs/articles/accounts/forbidden-usernames/#openpanel")
		os.Exit(1)
	}
	if len(u) > 20 {
		fmt.Println("[✘] Error: The username cannot be longer than 20 characters.")
		fmt.Println("       docs: https://openpanel.com/docs/articles/accounts/forbidden-usernames/#openpanel")
		os.Exit(1)
	}

	// Check forbidden list
	f, err := os.Open(forbiddenUsernamesFile)
	if err == nil {
		defer f.Close()
		scanner := bufio.NewScanner(f)
		for scanner.Scan() {
			forbidden := strings.TrimSpace(scanner.Text())
			if strings.EqualFold(u, forbidden) {
				fmt.Printf("[✘] Error: The username '%s' is not allowed.\n", u)
				fmt.Println("       docs: https://openpanel.com/docs/articles/accounts/forbidden-usernames/#reserved-usernames")
				os.Exit(1)
			}
		}
	}
}

// ─────────────────────────────────────────────────────────────
// Validate password against weakpass list
// ─────────────────────────────────────────────────────────────

func (c *Config) validatePasswordInLists(password string) {
	weakpass := readConfigValue(panelConfigFile, "weakpass")
	if weakpass == "" {
		c.log("weakpass value not found in openpanel.config. Defaulting to 'yes'.")
		weakpass = "yes"
	}
	if weakpass != "no" {
		return
	}

	c.log("Checking the password against weakpass dictionaries")
	dictPath := "/tmp/weakpass.txt"

	// Attempt to download dictionary
	_, err := runCmd("wget", "--timeout=5", "--tries=3", "-O", dictPath,
		"https://github.com/steveklabnik/password-cracker/blob/master/dictionary.txt")
	if err != nil || !fileExists(dictPath) {
		fmt.Println("[!] WARNING: Error downloading dictionary from https://weakpass.com/wordlist")
		return
	}
	defer os.Remove(dictPath)

	lower := strings.ToLower(password)
	f, err := os.Open(dictPath)
	if err != nil {
		return
	}
	defer f.Close()
	scanner := bufio.NewScanner(f)
	for scanner.Scan() {
		if strings.ToLower(strings.TrimSpace(scanner.Text())) == lower {
			fmt.Println("[✘] ERROR: password contains a common dictionary word from https://weakpass.com/wordlist")
			fmt.Println("       Please use stronger password or disable weakpass check with: 'opencli config update weakpass no'.")
			os.Exit(1)
		}
	}
}

// ─────────────────────────────────────────────────────────────
// Slave server validation
// ─────────────────────────────────────────────────────────────

func (c *Config) getSlaveIfSet() {
	if c.Server == "" {
		c.ContextFlag = ""
		h, _ := os.Hostname()
		c.Hostname = h
		return
	}
	if !isValidIPv4(c.Server) {
		fmt.Printf("ERROR: %s is not a valid IPv4 address (invalid format).\n", c.Server)
		os.Exit(1)
	}

	c.ContextFlag = "--context " + c.Server
	hostname, err := sshRun(c.KeyFlag, "root@"+c.Server, "hostname")
	if err != nil || hostname == "" {
		fmt.Printf("ERROR: Unable to reach the node %s - Exiting.\n", c.Server)
		fmt.Printf("       Make sure you can connect to the node from terminal with: 'ssh %s root@%s -vvv'\n", c.KeyFlag, c.Server)
		os.Exit(1)
	}
	c.NodeIPAddress = c.Server
	c.Hostname = hostname
	c.log(fmt.Sprintf("Container will be created on node: %s (%s)", c.NodeIPAddress, c.Hostname))
}

// ─────────────────────────────────────────────────────────────
// DB helpers (MySQL via shell)
// ─────────────────────────────────────────────────────────────

// mysqlQuery executes a MySQL query using credentials sourced from db.sh
// and returns the output. db.sh exports $config_file and $mysql_database.
func (c *Config) mysqlQuery(query string) (string, error) {
	script := fmt.Sprintf(
		`source %s && mysql --defaults-extra-file="$config_file" -D "$mysql_database" -se %q`,
		dbConfigFile, query,
	)
	return runCmd("bash", "-c", script)
}

// ─────────────────────────────────────────────────────────────
// Check existing user count (community edition limit)
// ─────────────────────────────────────────────────────────────

func (c *Config) getExistingUsersCount() {
	if c.KeyValue != "" {
		if c.Reseller == "" {
			c.log("Enterprise edition detected: unlimited number of users can be created")
		}
		return
	}
	if c.Reseller != "" {
		fmt.Println("[✘] ERROR: Resellers feature requires the Enterprise edition.")
		fmt.Println("If you require reseller accounts, please consider purchasing the Enterprise version that allows unlimited number of users and resellers.")
		os.Exit(1)
	}

	c.log("Checking if the limit of 3 users on Community edition is reached")
	out, err := c.mysqlQuery("SELECT COUNT(*) FROM users")
	if err != nil {
		fmt.Println("[✘] ERROR: Unable to get total user count from the database. Is mysql running?")
		os.Exit(1)
	}
	count, _ := strconv.Atoi(strings.TrimSpace(out))
	if count > 2 {
		fmt.Println("[✘] ERROR: OpenPanel Community edition has a limit of 3 user accounts - which should be enough for private use.")
		fmt.Println("If you require more than 3 accounts, please consider purchasing the Enterprise version that allows unlimited number of users and domains/websites.")
		os.Exit(1)
	}

	// Also check username uniqueness
	out2, err := c.mysqlQuery(fmt.Sprintf("SELECT COUNT(*) FROM users WHERE username = '%s'", c.Username))
	if err != nil {
		fmt.Println("[✘] Error: Unable to check username existence in the database. Is mysql running?")
		os.Exit(1)
	}
	if n, _ := strconv.Atoi(strings.TrimSpace(out2)); n > 0 {
		fmt.Printf("[✘] Error: Username '%s' is already taken.\n", c.Username)
		os.Exit(1)
	}
}

// ─────────────────────────────────────────────────────────────
// Plan info
// ─────────────────────────────────────────────────────────────

func (c *Config) getPlanInfoAndCheckRequirements() {
	c.log(fmt.Sprintf("Getting information from the database for plan %s", c.PlanName))
	query := fmt.Sprintf("SELECT cpu, ram, disk_limit, inodes_limit, bandwidth, id FROM plans WHERE name = '%s'", c.PlanName)
	out, err := c.mysqlQuery(query)
	if err != nil || strings.TrimSpace(out) == "" {
		fmt.Printf("[✘] ERROR: Plan with name %s not found. Unable to fetch CPU/RAM limits information from the database.\n", c.PlanName)
		os.Exit(1)
	}

	fields := strings.Fields(out)
	if len(fields) < 6 {
		fmt.Println("[✘] ERROR: Unable to parse plan information from the database.")
		os.Exit(1)
	}
	c.CPU = fields[0]
	c.RAM = fields[1]
	diskStr := strings.ReplaceAll(fields[2], "B", "")
	diskStr = strings.ReplaceAll(diskStr, " ", "")
	c.DiskLimit, _ = strconv.Atoi(diskStr)
	c.Inodes, _ = strconv.Atoi(fields[4])
	c.Bandwidth = fields[5]
	c.PlanID = fields[6] // note: index 6 is 7th field if disk has space — adjust if needed

	// Check CPU
	var maxCoresStr string
	if c.NodeIPAddress != "" {
		maxCoresStr, _ = sshRun(c.KeyFlag, "root@"+c.NodeIPAddress, "nproc")
	} else {
		maxCoresStr, _ = runCmd("nproc")
	}
	maxCores, _ := strconv.Atoi(strings.TrimSpace(maxCoresStr))
	cpuLimit, _ := strconv.Atoi(c.CPU)
	if cpuLimit > maxCores {
		fmt.Printf("[✘] ERROR: CPU cores (%d) limit on the plan exceed the maximum available cores on the server (%d). Cannot create user.\n", cpuLimit, maxCores)
		os.Exit(1)
	}

	// Check RAM
	var maxRAMStr string
	if c.NodeIPAddress != "" {
		maxRAMStr, _ = sshRun(c.KeyFlag, "root@"+c.NodeIPAddress, "free -g | awk '/Mem:/{print $2}'")
	} else {
		maxRAMStr, _ = runCmd("bash", "-c", "free -m | awk '/^Mem:/ {printf \"%d\\n\", ($2+512)/1024 }'")
	}
	maxRAM, _ := strconv.Atoi(strings.TrimSpace(maxRAMStr))
	ramStr := strings.TrimSuffix(c.RAM, "g")
	ramLimit, _ := strconv.Atoi(ramStr)
	if ramLimit > maxRAM {
		fmt.Printf("[✘] ERROR: RAM (%s GB) limit on the plan exceeds the maximum available RAM on the server (%d GB). Cannot create user.\n", c.RAM, maxRAM)
		os.Exit(1)
	}
}

// ─────────────────────────────────────────────────────────────
// Reseller checks
// ─────────────────────────────────────────────────────────────

func (c *Config) checkIfReseller() {
	if c.Reseller == "" {
		return
	}
	c.log("Checking if reseller user exists and can create new users..")

	out, _ := runCmd("sqlite3", adminDBFile,
		fmt.Sprintf("SELECT COUNT(*) FROM user WHERE username='%s' AND role='reseller';", c.Reseller))
	if n, _ := strconv.Atoi(strings.TrimSpace(out)); n < 1 {
		fmt.Printf("ERROR: User '%s' is not a reseller or not allowed to create new users. Contact support.\n", c.Reseller)
		os.Exit(1)
	}

	resellerLimitsFile := fmt.Sprintf("/etc/openpanel/openadmin/resellers/%s.json", c.Reseller)
	if !fileExists(resellerLimitsFile) {
		c.log(fmt.Sprintf("WARNING: Reseller %s has no limits configured and can create unlimited number of users.", c.Reseller))
		return
	}

	c.log("Checking reseller limits..")
	currentStr, err := c.mysqlQuery(fmt.Sprintf("SELECT COUNT(*) FROM users WHERE owner='%s'", c.Reseller))
	if err != nil {
		fmt.Println("ERROR: Unable to retrieve the number of users from the database. Is MySQL running?")
		os.Exit(1)
	}
	current, _ := strconv.Atoi(strings.TrimSpace(currentStr))
	c.CurrentAccounts = current

	// Update JSON with jq
	runCmdSilent("bash", "-c", fmt.Sprintf(
		`jq --argjson ca %d '.current_accounts = $ca' %q > /tmp/%s_config.json && mv /tmp/%s_config.json %q`,
		current, resellerLimitsFile, c.Reseller, c.Reseller, resellerLimitsFile,
	))

	maxAccStr, _ := runCmd("jq", "-r", `.max_accounts // "unlimited"`, resellerLimitsFile)
	c.MaxAccounts = strings.TrimSpace(maxAccStr)

	allowedPlans, _ := runCmd("jq", "-r", `.allowed_plans | join(",")`, resellerLimitsFile)

	if c.MaxAccounts != "unlimited" {
		maxN, _ := strconv.Atoi(c.MaxAccounts)
		if current >= maxN {
			fmt.Println("ERROR: Reseller has reached the maximum account limit. Cannot create more users.")
			os.Exit(1)
		}
	}

	planInAllowed := false
	for _, p := range strings.Split(allowedPlans, ",") {
		if strings.TrimSpace(p) == c.PlanID {
			planInAllowed = true
			break
		}
	}
	if !planInAllowed {
		fmt.Printf("ERROR: Current plan ID '%s' is not assigned for this reseller. Please select another plan.\n", c.PlanID)
		os.Exit(1)
	}
}

// ─────────────────────────────────────────────────────────────
// Debug info
// ─────────────────────────────────────────────────────────────

func (c *Config) printDebugInfoBeforeStartingCreation() {
	if !c.Debug {
		return
	}
	fmt.Println("--------------------------------------------------------------")
	if c.Reseller != "" {
		fmt.Println("Reseller user information:")
		fmt.Printf("- Reseller:             %s\n", c.Reseller)
		fmt.Printf("- Existing accounts:    %d/%s\n", c.CurrentAccounts, c.MaxAccounts)
		fmt.Println("--------------------------------------------------------------")
	}
	if c.NodeIPAddress != "" {
		fmt.Println("Data for connecting to the Node server:")
		fmt.Printf("- IP address:           %s\n", c.NodeIPAddress)
		fmt.Printf("- Hostname:             %s\n", c.Hostname)
		fmt.Println("- SSH user:             root")
		fmt.Printf("- SSH key path:         %s\n", c.Key)
		fmt.Println("--------------------------------------------------------------")
	}
	fmt.Println("Selected plan limits from database:")
	fmt.Printf("- plan id:           %s\n", c.PlanID)
	fmt.Printf("- plan name:         %s\n", c.PlanName)
	fmt.Printf("- cpu limit:         %s\n", c.CPU)
	fmt.Printf("- memory limit:      %s\n", c.RAM)
	if c.DiskLimit == 0 {
		fmt.Println("- storage:           unlimited")
	} else {
		fmt.Printf("- storage:           %d GB\n", c.DiskLimit)
	}
	if c.Inodes == 0 {
		fmt.Println("- inodes:            unlimited")
	} else {
		fmt.Printf("- inodes:            %d\n", c.Inodes)
	}
	fmt.Printf("- port speed:        %s\n", c.Bandwidth)
	fmt.Println("--------------------------------------------------------------")
}

// ─────────────────────────────────────────────────────────────
// SSH login validation
// ─────────────────────────────────────────────────────────────

func (c *Config) validateSSHLogin() {
	if c.NodeIPAddress == "" || !c.Debug {
		return
	}
	c.log(fmt.Sprintf("Validating SSH connection to the server %s", c.NodeIPAddress))
	if !fileExists(c.Key) {
		fmt.Printf("ERROR: Provided ssh key path: %s does not exist.\n", c.Key)
		os.Exit(1)
	}
	if fi, err := os.Stat(c.Key); err == nil {
		mode := fi.Mode().Perm()
		if mode != 0600 {
			c.log("SSH key permissions are incorrect. Correcting permissions to 600.")
			os.Chmod(c.Key, 0600)
		}
	}
	_, err := sshRun(c.KeyFlag, c.NodeIPAddress, "exit")
	if err != nil {
		fmt.Printf("ERROR: SSH connection failed to %s\n", c.NodeIPAddress)
		os.Exit(1)
	}
	c.log("SSH connection successfully established")
	runCmdSilent("csf", "-a", c.NodeIPAddress)
}

// ─────────────────────────────────────────────────────────────
// User creation + quota
// ─────────────────────────────────────────────────────────────

func (c *Config) createLocalUser() {
	c.log(fmt.Sprintf("Creating user %s", c.Username))
	if err := runCmdSilent("useradd", "-m", "-d", "/home/"+c.Username, c.Username); err != nil {
		fmt.Printf("Error: Failed creating linux user %s on master server.\n", c.Username)
		os.Exit(1)
	}
	out, err := runCmd("id", "-u", c.Username)
	if err != nil {
		fmt.Printf("Error: Failed to get UID for user %s.\n", c.Username)
		os.Exit(1)
	}
	c.UserID = strings.TrimSpace(out)
}

func (c *Config) createRemoteUser(providedID string) {
	if c.NodeIPAddress == "" {
		return
	}
	idFlag := ""
	if providedID != "" {
		idFlag = "-u " + providedID
	}
	c.log(fmt.Sprintf("Creating user %s on server %s", c.Username, c.NodeIPAddress))
	cmd := fmt.Sprintf("useradd -m -s /bin/bash -d /home/%s %s %s", c.Username, idFlag, c.Username)
	sshRunSilent(c.KeyFlag, "root@"+c.NodeIPAddress, cmd)

	uid, err := sshRun(c.KeyFlag, "root@"+c.NodeIPAddress, "id -u "+c.Username)
	if err != nil {
		fmt.Printf("Error: Failed creating linux user %s on node: %s\n", c.Username, c.NodeIPAddress)
		os.Exit(1)
	}
	c.UserID = strings.TrimSpace(uid)
}

func (c *Config) setUserQuota() {
	if c.DiskLimit != 0 {
		storageInBlocks := c.DiskLimit * 1024000
		c.log(fmt.Sprintf("Setting storage size of %dGB and %d inodes for the user", c.DiskLimit, c.Inodes))
		runCmdSilent("setquota", "-u", c.Username,
			strconv.Itoa(storageInBlocks), strconv.Itoa(storageInBlocks),
			strconv.Itoa(c.Inodes), strconv.Itoa(c.Inodes), "/")
	} else {
		c.log("Setting unlimited storage and inodes for the user")
		runCmdSilent("setquota", "-u", c.Username, "0", "0", "0", "0", "/")
	}
}

func (c *Config) createUserSetQuotaAndPassword() {
	c.createLocalUser()
	c.createRemoteUser(c.UserID)
	c.setUserQuota()
}

// ─────────────────────────────────────────────────────────────
// SSHFS mounts
// ─────────────────────────────────────────────────────────────

func (c *Config) sshfsMounts() {
	if c.NodeIPAddress == "" {
		return
	}

	// Step 1 – ensure sshd config on slave
	step1 := `if [ ! -d "/etc/openpanel/openpanel" ]; then
  echo "PubkeyAuthentication yes" >> /etc/ssh/sshd_config
  echo "AuthorizedKeysFile     .ssh/authorized_keys" >> /etc/ssh/sshd_config
  service ssh restart > /dev/null 2>&1
fi`
	sshRunSilent(c.KeyFlag, "root@"+c.NodeIPAddress, step1)
	time.Sleep(5 * time.Second)

	// Step 2 – install uidmap on slave
	step2 := `if [ ! -d "/etc/openpanel/openpanel" ]; then
  echo "Node is not yet configured to be used as an OpenPanel slave server. Configuring.."
  if command -v apt-get &> /dev/null; then
    export DEBIAN_FRONTEND=noninteractive
    apt-get update > /dev/null 2>&1 && apt-get -yq install systemd-container uidmap > /dev/null 2>&1
  elif command -v dnf &> /dev/null; then
    dnf install -y systemd-container uidmap > /dev/null 2>&1
  elif command -v yum &> /dev/null; then
    yum install -y systemd-container uidmap > /dev/null 2>&1
  else
    echo "[✘] ERROR: Unable to setup the slave server. Contact support."
    exit 1
  fi
fi`
	sshRunSilent(c.KeyFlag, "root@"+c.NodeIPAddress, step2)

	// Step 3 – cgroup delegation
	step3 := `if [ ! -d "/etc/openpanel/openpanel" ]; then
  echo "Adding permissions for users to limit CPU% - more info: https://docs.docker.com/engine/security/rootless/#limiting-resources"
  mkdir -p /etc/systemd/system/user@.service.d
  cat > /etc/systemd/system/user@.service.d/delegate.conf << 'INNER_EOF'
[Service]
Delegate=cpu cpuset io memory pids
INNER_EOF
  systemctl daemon-reload
fi`
	sshRunSilent(c.KeyFlag, "root@"+c.NodeIPAddress, step3)

	// scp /etc/openpanel to slave
	scpArgs := []string{}
	if c.KeyFlag != "" {
		scpArgs = append(scpArgs, strings.Fields(c.KeyFlag)...)
	}
	scpArgs = append(scpArgs, "-r", "/etc/openpanel",
		fmt.Sprintf("root@%s:/etc/openpanel", c.NodeIPAddress))
	runCmd("scp", scpArgs...)

	// Ensure sshfs on master
	if _, err := runCmd("command", "-v", "sshfs"); err != nil {
		if _, err2 := runCmd("command", "-v", "apt-get"); err2 == nil {
			runCmd("apt-get", "install", "-y", "sshfs")
		} else if _, err2 := runCmd("command", "-v", "dnf"); err2 == nil {
			runCmd("dnf", "install", "-y", "sshfs")
		} else if _, err2 := runCmd("command", "-v", "yum"); err2 == nil {
			runCmd("yum", "install", "-y", "sshfs")
		} else {
			fmt.Println("[✘] ERROR: Unable to setup sshfs on master server. Contact support.")
			os.Exit(1)
		}
	}

	// Mount home dir
	keyArg := ""
	if c.Key != "" {
		keyArg = "IdentityFile=" + c.Key
	}
	sshfsArgs := []string{
		"-o", "StrictHostKeyChecking=no",
	}
	if keyArg != "" {
		sshfsArgs = append(sshfsArgs, "-o", keyArg)
	}
	sshfsArgs = append(sshfsArgs,
		fmt.Sprintf("root@%s:/home/%s", c.NodeIPAddress, c.Username),
		fmt.Sprintf("/home/%s", c.Username),
	)
	runCmd("sshfs", sshfsArgs...)
}

// ─────────────────────────────────────────────────────────────
// SSH key setup
// ─────────────────────────────────────────────────────────────

func (c *Config) setupSSHKey() {
	if c.NodeIPAddress == "" {
		return
	}
	c.log("Setting ssh key..")

	publicKey, err := runCmd("ssh-keygen", "-y", "-f", c.Key)
	if err != nil {
		c.log("Warning: could not derive public key from " + c.Key)
	}

	remoteCmd := fmt.Sprintf(`mkdir -p /home/%s/.ssh > /dev/null 2>&1
touch /home/%s/.ssh/authorized_keys > /dev/null 2>&1
chown %s -R /home/%s/.ssh > /dev/null 2>&1
if ! grep -q "%s" /home/%s/.ssh/authorized_keys 2>/dev/null; then
  echo "%s" >> /home/%s/.ssh/authorized_keys
fi`, c.Username, c.Username, c.Username, c.Username,
		publicKey, c.Username, publicKey, c.Username)
	sshRunSilent(c.KeyFlag, "root@"+c.NodeIPAddress, remoteCmd)

	os.MkdirAll(os.Getenv("HOME")+"/.ssh", 0700)
	dest := fmt.Sprintf("%s/.ssh/%s", os.Getenv("HOME"), c.NodeIPAddress)
	runCmdSilent("cp", c.Key, dest)
	os.Chmod(dest, 0600)

	sshConfig := fmt.Sprintf(`
Host %s
    HostName %s
    User %s
    IdentityFile ~/.ssh/%s
    StrictHostKeyChecking no
    UserKnownHostsFile /dev/null
    ControlPath ~/.ssh/cm_socket/%%r@%%h:%%p
    ControlMaster auto
    ControlPersist 30s
`, c.Username, c.NodeIPAddress, c.Username, c.NodeIPAddress)

	os.MkdirAll(os.Getenv("HOME")+"/.ssh/cm_socket", 0700)
	f, _ := os.OpenFile(os.Getenv("HOME")+"/.ssh/config", os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0600)
	if f != nil {
		f.WriteString(sshConfig)
		f.Close()
	}

	_, err = sshRun("", c.Username, "exit")
	if err != nil {
		fmt.Println("ERROR: Failed to establish SSH connection to the newly created user.")
		os.Exit(1)
	}
	c.log("SSH connection successfully established")
}

// ─────────────────────────────────────────────────────────────
// Docker install on node
// ─────────────────────────────────────────────────────────────

func (c *Config) installDockerAndAddUser() {
	if c.NodeIPAddress == "" {
		return
	}
	c.log(fmt.Sprintf("Checking if Docker is installed on %s...", c.NodeIPAddress))

	_, err := sshRun(c.KeyFlag, "root@"+c.NodeIPAddress, "command -v docker >/dev/null 2>&1")
	if err != nil {
		c.log(fmt.Sprintf("Docker is not installed. Installing Docker on %s...", c.NodeIPAddress))
		sshRunSilent(c.KeyFlag, "root@"+c.NodeIPAddress,
			`set -e; apt update; apt install -y docker.io; systemctl enable --now docker`)
		c.log("Docker installed on destination server.")
	} else {
		c.log("Docker is already installed on destination server.")
	}

	c.log(fmt.Sprintf("Adding user '%s' to docker group on %s...", c.Username, c.NodeIPAddress))
	sshRunSilent(c.KeyFlag, "root@"+c.NodeIPAddress, fmt.Sprintf(
		`if id -nG "%s" | grep -qw docker; then :; else usermod -aG docker "%s" && echo "User %s added to docker group."; fi`,
		c.Username, c.Username, c.Username,
	))
}

// ─────────────────────────────────────────────────────────────
// Create volume
// ─────────────────────────────────────────────────────────────

func (c *Config) createVolume() {
	volPath := fmt.Sprintf("/home/%s/docker-data/volumes/%s_html_data/_data/", c.Username, c.Username)
	os.MkdirAll(volPath, 0755)
	runCmdSilent("chown", c.Username+":"+c.Username, volPath)
	runCmdSilent("chmod", "-R", "g+w", volPath)
}

// ─────────────────────────────────────────────────────────────
// Docker Compose binary setup
// ─────────────────────────────────────────────────────────────

func (c *Config) dockerCompose() {
	const (
		armLink = "https://github.com/docker/compose/releases/download/v2.36.0/docker-compose-linux-aarch64"
		x86Link = "https://github.com/docker/compose/releases/download/v2.36.0/docker-compose-linux-x86_64"
	)
	if c.NodeIPAddress != "" {
		c.log(fmt.Sprintf("Configuring Docker Compose for user %s on node %s", c.Username, c.NodeIPAddress))
		sshRunSilent(c.KeyFlag, "root@"+c.NodeIPAddress, fmt.Sprintf(
			`su - %s -c 'DOCKER_CONFIG=${DOCKER_CONFIG:-/home/%s/.docker}
mkdir -p /home/%s/.docker/cli-plugins
curl -sSL %s -o /home/%s/.docker/cli-plugins/docker-compose
chmod +x /home/%s/.docker/cli-plugins/docker-compose'`,
			c.Username, c.Username, c.Username, x86Link, c.Username, c.Username,
		))
		return
	}

	archOut, _ := runCmd("bash", "-c", "lscpu | grep Architecture | awk '{print $2}'")
	arch := strings.TrimSpace(archOut)

	var systemWideFile, link string
	if arch == "aarch64" {
		c.log("Setting compose for ARM CPU (/etc/openpanel/docker/docker-compose-linux-aarch64)")
		systemWideFile = "/etc/openpanel/docker/docker-compose-linux-aarch64"
		link = armLink
	} else {
		c.log("Setting compose for x86_64 CPU (/etc/openpanel/docker/docker-compose-linux-x86_64)")
		systemWideFile = "/etc/openpanel/docker/docker-compose-linux-x86_64"
		link = x86Link
	}

	if !fileExists(systemWideFile) {
		runCmd("curl", "-sSL", link, "-o", systemWideFile)
	}
	os.Chmod(systemWideFile, 0755)
	os.MkdirAll(fmt.Sprintf("/home/%s/.docker/cli-plugins", c.Username), 0755)
	runCmdSilent("ln", "-sf", systemWideFile,
		fmt.Sprintf("/home/%s/.docker/cli-plugins/docker-compose", c.Username))
}

// ─────────────────────────────────────────────────────────────
// Docker rootless setup
// ─────────────────────────────────────────────────────────────

func (c *Config) dockerRootless() {
	c.log("Configuring Docker in Rootless mode")

	// Setup dirs
	for _, d := range []string{
		fmt.Sprintf("/home/%s/docker-data", c.Username),
		fmt.Sprintf("/home/%s/.config/docker", c.Username),
	} {
		os.MkdirAll(d, 0755)
	}

	// Copy daemon.json
	daemonSrc := "/etc/openpanel/docker/daemon/rootless.json"
	daemonDst := fmt.Sprintf("/home/%s/.config/docker/daemon.json", c.Username)
	if fileExists(daemonSrc) {
		data, _ := os.ReadFile(daemonSrc)
		content := strings.ReplaceAll(string(data), "USERNAME", c.Username)
		os.WriteFile(daemonDst, []byte(content), 0644)
	}

	os.MkdirAll(fmt.Sprintf("/home/%s/bin", c.Username), 0755)
	runCmdSilent("chmod", "755", "-R", fmt.Sprintf("/home/%s/", c.Username))

	bashrc := fmt.Sprintf("/home/%s/.bashrc", c.Username)
	if fileExists(bashrc) {
		data, _ := os.ReadFile(bashrc)
		prepend := fmt.Sprintf("export PATH=/home/%s/bin:$PATH\n", c.Username)
		os.WriteFile(bashrc, append([]byte(prepend), data...), 0644)
	}

	if c.NodeIPAddress != "" {
		c.dockerRootlessRemote()
	} else {
		c.dockerRootlessLocal()
	}
}

func (c *Config) dockerRootlessRemote() {
	// AppArmor + service setup on remote (mirrors the bash heredoc blocks)
	apparmorScript := fmt.Sprintf(`
cat > "/etc/apparmor.d/home.%s.bin.rootlesskit" << 'EOT1'
abi <abi/4.0>,
include <tunables/global>

  /home/%s/bin/rootlesskit flags=(unconfined) {
    userns,
    include if exists <local/home.%s.bin.rootlesskit>
  }
EOT1

filename=$(echo "/home/%s/bin/rootlesskit" | sed -e 's@^/@@' -e 's@/@.@g')
cat > "/etc/apparmor.d/${filename}" << EOT2
abi <abi/4.0>,
include <tunables/global>

  "/home/%s/bin/rootlesskit" flags=(unconfined) {
    userns,
    include if exists <local/${filename}>
  }
EOT2
`, c.Username, c.Username, c.Username, c.Username, c.Username)
	sshRunSilent(c.KeyFlag, "root@"+c.NodeIPAddress, apparmorScript)

	c.log("Restarting services..")
	sshRunSilent(c.KeyFlag, "root@"+c.NodeIPAddress, fmt.Sprintf(
		`systemctl restart apparmor.service >/dev/null 2>&1
loginctl enable-linger %s >/dev/null 2>&1
mkdir -p /home/%s/.docker/run >/dev/null 2>&1
chmod 700 /home/%s/.docker/run >/dev/null 2>&1
chmod 755 -R /home/%s/ >/dev/null 2>&1
chown -R %s:%s /home/%s/ >/dev/null 2>&1`,
		c.Username, c.Username, c.Username, c.Username, c.Username, c.Username, c.Username,
	))

	c.log("Downloading https://get.docker.com/rootless")
	sshRunSilent(c.KeyFlag, "root@"+c.NodeIPAddress, fmt.Sprintf(
		`su - %s -c 'bash -l -c "
cd /home/%s/bin
wget --timeout=5 --tries=3 -O /home/%s/bin/dockerd-rootless-setuptool.sh https://get.docker.com/rootless > /dev/null 2>&1
chmod +x /home/%s/bin/dockerd-rootless-setuptool.sh
/home/%s/bin/dockerd-rootless-setuptool.sh install > /dev/null 2>&1
echo \"export XDG_RUNTIME_DIR=/home/%s/.docker/run\" >> ~/.bashrc
echo \"export DBUS_SESSION_BUS_ADDRESS=unix:path=/run/user/\$(id -u)/bus\" >> ~/.bashrc
"'`,
		c.Username, c.Username, c.Username, c.Username, c.Username, c.Username,
	))

	c.log("Configuring Docker service..")
	dockerService := fmt.Sprintf(`mkdir -p ~/.config/systemd/user/
cat > ~/.config/systemd/user/docker.service << 'EOF'
[Unit]
Description=Docker Application Container Engine (Rootless)
After=network.target

[Service]
Environment=PATH=/home/%s/bin:$PATH
Environment=DOCKER_HOST=unix:///home/%s/.docker/run/docker.sock
ExecStart=/home/%s/bin/dockerd-rootless.sh -H unix:///home/%s/.docker/run/docker.sock

Restart=on-failure
StartLimitBurst=3
StartLimitInterval=60s

[Install]
WantedBy=default.target
EOF`, c.Username, c.Username, c.Username, c.Username)
	sshRun("", c.Username, dockerService)

	c.log("Starting user services..")
	sshRunSilent(c.KeyFlag, "root@"+c.NodeIPAddress, fmt.Sprintf(
		`machinectl shell %s@ /bin/bash -c '
systemctl --user daemon-reload > /dev/null 2>&1
systemctl --user enable docker > /dev/null 2>&1
systemctl --user start docker > /dev/null 2>&1
' 2>/dev/null`, c.Username,
	))
}

func (c *Config) dockerRootlessLocal() {
	// AppArmor profile
	apparmorContent := fmt.Sprintf(`# ref: https://ubuntu.com/blog/ubuntu-23-10-restricted-unprivileged-user-namespaces
abi <abi/4.0>,
include <tunables/global>

/home/%s/bin/rootlesskit flags=(unconfined) {
userns,
include if exists <local/home.%s.bin.rootlesskit>
}
`, c.Username, c.Username)
	apparmorPath := fmt.Sprintf("/etc/apparmor.d/home.%s.bin.rootlesskit", c.Username)
	os.WriteFile(apparmorPath, []byte(apparmorContent), 0644)

	homeDir := os.Getenv("HOME")
	if homeDir == "" {
		homeDir = "/root"
	}
	filenameRaw := strings.TrimPrefix(homeDir+"/bin/rootlesskit", "/")
	filename := strings.ReplaceAll(filenameRaw, "/", ".")
	profile := fmt.Sprintf(`abi <abi/4.0>,
include <tunables/global>

"%s/bin/rootlesskit" flags=(unconfined) {
userns,
include if exists <local/%s>
}
`, homeDir, filename)
	tmpPath := filepath.Join(homeDir, filename)
	os.WriteFile(tmpPath, []byte(profile), 0644)
	runCmdSilent("mv", tmpPath, "/etc/apparmor.d/"+filename)

	runCmdSilent("systemctl", "restart", "apparmor.service")
	runCmdSilent("loginctl", "enable-linger", c.Username)

	for _, d := range []string{
		fmt.Sprintf("/home/%s/.docker/run", c.Username),
		fmt.Sprintf("/home/%s/bin", c.Username),
		fmt.Sprintf("/home/%s/bin/.config/systemd/user/", c.Username),
	} {
		os.MkdirAll(d, 0755)
	}
	os.Chmod(fmt.Sprintf("/home/%s/.docker/run", c.Username), 0700)
	runCmdSilent("chmod", "755", "-R", fmt.Sprintf("/home/%s/", c.Username))
	runCmdSilent("chown", "-R", c.Username+":"+c.Username, fmt.Sprintf("/home/%s/", c.Username))

	systemWideScript := "/etc/openpanel/docker/dockerd-rootless-setuptool.sh"
	if !fileExists(systemWideScript) {
		runCmd("curl", "-sSL", "https://get.docker.com/rootless", "-o", systemWideScript)
		os.Chmod(systemWideScript, 0755)
	}
	runCmdSilent("ln", "-sf", systemWideScript,
		fmt.Sprintf("/home/%s/bin/dockerd-rootless-setuptool.sh", c.Username))

	installScript := fmt.Sprintf(`
source ~/.bashrc
/home/%s/bin/dockerd-rootless-setuptool.sh install >/dev/null 2>&1
echo 'export XDG_RUNTIME_DIR=/home/%s/.docker/run' >> ~/.bashrc
echo 'export PATH=/home/%s/bin:$PATH' >> ~/.bashrc
echo 'export DOCKER_HOST=unix:///home/%s/.docker/run/docker.sock' >> ~/.bashrc
source ~/.bashrc
mkdir -p ~/.config/systemd/user/
cat > ~/.config/systemd/user/docker.service << 'EOF'
[Unit]
Description=Docker Application Container Engine (Rootless)
After=network.target

[Service]
Environment=PATH=/home/%s/bin:$PATH
Environment=DOCKER_HOST=unix://%%t/docker.sock
ExecStart=/home/%s/bin/dockerd-rootless.sh
Restart=on-failure
StartLimitBurst=3
StartLimitInterval=60s

[Install]
WantedBy=default.target
EOF
systemctl --user daemon-reload > /dev/null 2>&1
systemctl --user restart docker > /dev/null 2>&1
`, c.Username, c.Username, c.Username, c.Username, c.Username, c.Username)

	runCmdSilent("bash", "-c",
		fmt.Sprintf(`machinectl shell %s@ /bin/bash -c %q 2>/dev/null`, c.Username, installScript))
}

// ─────────────────────────────────────────────────────────────
// Docker context
// ─────────────────────────────────────────────────────────────

func (c *Config) createContext() {
	var host string
	if c.NodeIPAddress != "" {
		host = fmt.Sprintf("host=ssh://%s", c.Username)
	} else {
		host = fmt.Sprintf("host=unix:///hostfs/run/user/%s/docker.sock", c.UserID)
	}
	runCmd("docker", "context", "create", c.Username, "--docker", host, "--description", c.Username)
}

func (c *Config) testComposeCommandForUser() {
	_, err := runCmd("docker", "--context="+c.Username, "compose", "version")
	if err != nil {
		fmt.Println("[✘] Error: Docker Compose is not working in this context. User creation failed.")
		c.hardCleanup()
	}
}

// ─────────────────────────────────────────────────────────────
// PHP version
// ─────────────────────────────────────────────────────────────

func (c *Config) getPHPVersion() {
	envFile := "/etc/openpanel/docker/compose/1.0/.env"
	val := readConfigValue(envFile, "DEFAULT_PHP_VERSION")
	// strip quotes
	val = strings.Trim(val, `"'`)
	if val == "" {
		c.log("Default PHP version not found in .env file, using the fallback default version..")
		val = "8.4"
	}
	c.DefaultPHPVersion = val
}

// ─────────────────────────────────────────────────────────────
// Port assignment & docker-compose setup
// ─────────────────────────────────────────────────────────────

func (c *Config) findAvailablePorts() [7]int {
	// Get highest port used by last created user
	out, _ := c.mysqlQuery("SELECT server FROM users ORDER BY id DESC LIMIT 1")
	lastUser := strings.TrimSpace(out)
	minPort := 32768

	if lastUser != "" {
		envFile := fmt.Sprintf("/home/%s/.env", lastUser)
		if fileExists(envFile) {
			data, _ := os.ReadFile(envFile)
			re := regexp.MustCompile(`(?m)^[A-Z_]+_PORT=(\d+)`)
			matches := re.FindAllStringSubmatch(string(data), -1)
			highest := 0
			for _, m := range matches {
				v, _ := strconv.Atoi(m[1])
				if v > highest {
					highest = v
				}
			}
			if highest > 0 {
				minPort = highest
			}
		}
	}

	var ports [7]int
	for i := 0; i < 7; i++ {
		ports[i] = minPort + i + 1
	}
	return ports
}

func (c *Config) runDocker() {
	ports := c.findAvailablePorts()
	c.log("Checking available ports to use for the user")

	if c.NodeIPAddress != "" {
		c.Ports[0] = fmt.Sprintf("%s:%d:80", c.NodeIPAddress, ports[0])
		c.Ports[1] = fmt.Sprintf("%s:%d:3306", c.NodeIPAddress, ports[1])
		c.Ports[2] = fmt.Sprintf("%s:%d:5432", c.NodeIPAddress, ports[2])
		c.Ports[3] = fmt.Sprintf("%s:%d:80", c.NodeIPAddress, ports[3])
		c.Ports[4] = fmt.Sprintf("%s:%d:80", c.NodeIPAddress, ports[4])
		c.Ports[5] = fmt.Sprintf("%s:%d:443", c.NodeIPAddress, ports[5])
		c.Ports[6] = fmt.Sprintf("%s:%d:80", c.NodeIPAddress, ports[6])
	} else {
		c.Ports[0] = fmt.Sprintf("%d:80", ports[0])
		c.Ports[1] = fmt.Sprintf("%d:3306", ports[1])
		c.Ports[2] = fmt.Sprintf("%d:5432", ports[2])
		c.Ports[3] = fmt.Sprintf("%d:80", ports[3])
		c.Ports[4] = fmt.Sprintf("127.0.0.1:%d:80", ports[4])
		c.Ports[5] = fmt.Sprintf("127.0.0.1:%d:443", ports[5])
		c.Ports[6] = fmt.Sprintf("127.0.0.1:%d:80", ports[6])
	}

	// Copy docker-compose.yml
	src := "/etc/openpanel/docker/compose/1.0/docker-compose.yml"
	dst := fmt.Sprintf("/home/%s/docker-compose.yml", c.Username)
	if !fileExists(src) {
		fmt.Printf("ERROR: %s not found. Make sure /etc/openpanel/ is updated.\n", src)
		os.Exit(1)
	}
	data, _ := os.ReadFile(src)
	os.WriteFile(dst, data, 0644)

	postgresPassword := randomBase64(12)
	mysqlRootPassword := randomBase64(12)
	pgAdminPassword := randomBase64(12)

	required := []string{c.Username, c.UserID, c.CPU, c.RAM,
		c.Ports[4], c.Ports[5], c.Ports[6],
		c.Ports[0], c.Ports[2], c.Ports[3], c.Ports[1],
		c.DefaultPHPVersion, postgresPassword, mysqlRootPassword}
	for _, v := range required {
		if v == "" {
			fmt.Println("ERROR: One or more required variables are not set.")
			os.Exit(1)
		}
	}

	envSrc := "/etc/openpanel/docker/compose/1.0/.env"
	envDst := fmt.Sprintf("/home/%s/.env", c.Username)
	envData, _ := os.ReadFile(envSrc)
	env := string(envData)

	replacements := map[string]string{
		`USERNAME="[^"]*"`:                                fmt.Sprintf(`USERNAME="%s"`, c.Username),
		`USER_ID="[^"]*"`:                                 fmt.Sprintf(`USER_ID="%s"`, c.UserID),
		`CONTEXT="[^"]*"`:                                 fmt.Sprintf(`CONTEXT="%s"`, c.Username),
		`TOTAL_CPU="[^"]*"`:                               fmt.Sprintf(`TOTAL_CPU="%s"`, c.CPU),
		`TOTAL_RAM="[^"]*"`:                               fmt.Sprintf(`TOTAL_RAM="%s"`, c.RAM),
		`(?m)^HTTP_PORT="[^"]*"`:                          fmt.Sprintf(`HTTP_PORT="%s"`, c.Ports[4]),
		`HTTPS_PORT="[^"]*"`:                              fmt.Sprintf(`HTTPS_PORT="%s"`, c.Ports[5]),
		`PGADMIN_PORT="[^"]*"`:                            fmt.Sprintf(`PGADMIN_PORT="%s"`, c.Ports[0]),
		`POSTGRES_PORT="[^"]*"`:                           fmt.Sprintf(`POSTGRES_PORT="127.0.0.1:%s"`, c.Ports[2]),
		`PMA_PORT="[^"]*"`:                                fmt.Sprintf(`PMA_PORT="%s"`, c.Ports[3]),
		`\{PMA_PORT\}`:                                    strconv.Itoa(ports[3]),
		`POSTGRES_PASSWORD="[^"]*"`:                       fmt.Sprintf(`POSTGRES_PASSWORD="%s"`, postgresPassword),
		`PGADMIN_PW=[^\n"]*`:                              fmt.Sprintf(`PGADMIN_PW=%s`, pgAdminPassword),
		`OPENSEARCH_INITIAL_ADMIN_PASSWORD="[^"]*"`:       fmt.Sprintf(`OPENSEARCH_INITIAL_ADMIN_PASSWORD="%s"`, pgAdminPassword),
		`MYSQL_PORT="[^"]*"`:                              fmt.Sprintf(`MYSQL_PORT="127.0.0.1:%s"`, c.Ports[1]),
		`DEFAULT_PHP_VERSION="[^"]*"`:                     fmt.Sprintf(`DEFAULT_PHP_VERSION="%s"`, c.DefaultPHPVersion),
		`MYSQL_ROOT_PASSWORD="[^"]*"`:                     fmt.Sprintf(`MYSQL_ROOT_PASSWORD="%s"`, mysqlRootPassword),
		`PROXY_HTTP_PORT="[^"]*"`:                         fmt.Sprintf(`#PROXY_HTTP_PORT="%s"`, c.Ports[6]),
	}

	for pattern, replacement := range replacements {
		re := regexp.MustCompile(pattern)
		env = re.ReplaceAllString(env, replacement)
	}

	if isValidEmail(c.Email) {
		re := regexp.MustCompile(`PGADMIN_MAIL=[^\n]*`)
		env = re.ReplaceAllString(env, "PGADMIN_MAIL="+c.Email)
	}

	// Webserver
	if c.Webserver != "" {
		varnishRe := regexp.MustCompile(`^varnish\+([a-zA-Z]+)$`)
		validWS := map[string]bool{"nginx": true, "apache": true, "openresty": true, "openlitespeed": true, "litespeed": true}
		if m := varnishRe.FindStringSubmatch(c.Webserver); m != nil {
			ws := m[1]
			c.log(fmt.Sprintf("Setting varnish caching and %s as webserver for the user..", ws))
			re2 := regexp.MustCompile(`WEB_SERVER="[^"]*"`)
			env = re2.ReplaceAllString(env, fmt.Sprintf(`WEB_SERVER="%s"`, ws))
			re3 := regexp.MustCompile(`(?m)^#PROXY_HTTP_PORT`)
			env = re3.ReplaceAllString(env, "PROXY_HTTP_PORT")
		} else if validWS[c.Webserver] {
			c.log(fmt.Sprintf("Setting %s as webserver for the user..", c.Webserver))
			re2 := regexp.MustCompile(`WEB_SERVER="[^"]*"`)
			env = re2.ReplaceAllString(env, fmt.Sprintf(`WEB_SERVER="%s"`, c.Webserver))
		} else {
			c.log(fmt.Sprintf("Warning: invalid webserver type selected: %s. Using the default instead..", c.Webserver))
		}
	}

	if c.SQLType != "" {
		if c.SQLType == "mysql" || c.SQLType == "mariadb" {
			c.log(fmt.Sprintf("Setting %s as MySQL server type for the user..", c.SQLType))
			re2 := regexp.MustCompile(`MYSQL_TYPE="[^"]*"`)
			env = re2.ReplaceAllString(env, fmt.Sprintf(`MYSQL_TYPE="%s"`, c.SQLType))
		} else {
			c.log(fmt.Sprintf("Warning: Invalid SQL server type selected: %s. Using the default instead..", c.SQLType))
		}
	}

	os.WriteFile(envDst, []byte(env), 0644)
	if !fileExists(envDst) {
		fmt.Println("ERROR: Failed to create .env file! Make sure /etc/openpanel/ is updated.")
		os.Exit(1)
	}

	// Sockets and config files
	for _, d := range []string{"mysqld", "postgres", "redis", "memcached"} {
		os.MkdirAll(fmt.Sprintf("/home/%s/sockets/%s", c.Username, d), 0777)
	}
	runCmdSilent("chown", c.Username+":"+c.Username, fmt.Sprintf("/home/%s/sockets", c.Username))
	runCmdSilent("chmod", "777", fmt.Sprintf("/home/%s/sockets/", c.Username))

	copyIfExists := func(src, dst string) {
		if data, err := os.ReadFile(src); err == nil {
			os.WriteFile(dst, data, 0644)
		}
	}
	home := fmt.Sprintf("/home/%s", c.Username)
	copyIfExists("/etc/openpanel/mysql/user.cnf", home+"/custom.cnf")
	copyIfExists("/etc/openpanel/postgres/postgresql.conf", home+"/postgre_custom.conf")
	copyIfExists("/etc/openpanel/nginx/user-nginx.conf", home+"/nginx.conf")
	copyIfExists("/etc/openpanel/openresty/nginx.conf", home+"/openresty.conf")
	copyIfExists("/etc/openpanel/openlitespeed/httpd_config.conf", home+"/openlitespeed.conf")
	copyIfExists("/etc/openpanel/apache/httpd.conf", home+"/httpd.conf")
	copyIfExists("/etc/openpanel/varnish/default.vcl", home+"/default.vcl")
	copyIfExists("/etc/openpanel/ofelia/users.ini", home+"/crons.ini")
	copyIfExists("/etc/openpanel/backups/backup.env", home+"/backup.env")
	copyIfExists("/etc/openpanel/mysql/phpmyadmin/pma.php", home+"/pma.php")

	// php.ini dir
	runCmdSilent("cp", "-r", "/etc/openpanel/php/ini", home+"/php.ini")

	// my.cnf
	myCnf := fmt.Sprintf("[client]\nuser=root\npassword=%s\n", mysqlRootPassword)
	os.WriteFile(home+"/my.cnf", []byte(myCnf), 0600)
}

// ─────────────────────────────────────────────────────────────
// Quota reload
// ─────────────────────────────────────────────────────────────

func (c *Config) reloadUserQuotas() {
	runCmdSilent("quotacheck", "-avm")
	os.MkdirAll("/etc/openpanel/openpanel/core/users/", 0755)
	runCmd("bash", "-c", "repquota -u / > /etc/openpanel/openpanel/core/users/repquota")
}

// ─────────────────────────────────────────────────────────────
// Password hash
// ─────────────────────────────────────────────────────────────

func (c *Config) generateUserPasswordHash() {
	if c.Password == "generate" {
		c.Password = generatePassword()
		c.log(fmt.Sprintf("Generated password: %s", c.Password))
	}

	// Use werkzeug via system or venv python3
	script := fmt.Sprintf(
		`from werkzeug.security import generate_password_hash; print(generate_password_hash('%s'))`,
		c.Password,
	)
	var out string
	var err error
	if fileExists("/usr/local/admin/venv/bin/python3") {
		out, err = runCmd("/usr/local/admin/venv/bin/python3", "-c", script)
	} else {
		out, err = runCmd("python3", "-c", script)
	}
	if err != nil || out == "" {
		fmt.Println("Warning: No Python 3 interpreter found. Please install Python 3 or check the virtual environment.")
		os.Exit(1)
	}
	c.HashedPassword = strings.TrimSpace(out)
}

// ─────────────────────────────────────────────────────────────
// Skeleton files
// ─────────────────────────────────────────────────────────────

func (c *Config) copySkeletonFiles() {
	c.log("Creating configuration files for the newly created user")
	dst := fmt.Sprintf("/etc/openpanel/openpanel/core/users/%s/", c.Username)
	os.RemoveAll("/etc/openpanel/skeleton/domains")
	runCmdSilent("cp", "-r", "/etc/openpanel/skeleton/", dst)

	if c.NodeIPAddress != "" {
		jsonFile := dst + "ip.json"
		os.WriteFile(jsonFile, []byte(fmt.Sprintf(`{ "ip": "%s" }`, c.NodeIPAddress)), 0644)
	}

	// Run php-available_versions in background
	cmd := exec.Command("opencli", "php-available_versions", c.Username)
	cmd.Stdout = nil
	cmd.Stderr = nil
	cmd.Start()
}

// ─────────────────────────────────────────────────────────────
// Image pre-pull
// ─────────────────────────────────────────────────────────────

func getEnvValue(envFile, key string) string {
	val := readConfigValue(envFile, key)
	val = strings.TrimSuffix(val, "\r")
	val = strings.Trim(val, `"'`)
	return val
}

func (c *Config) downloadImages() {
	if c.SkipImagePull {
		c.log("Skipping image pull due to the '--skip-images' flag.")
		return
	}
	envFile := fmt.Sprintf("/home/%s/.env", c.Username)
	if !fileExists(envFile) {
		fmt.Printf("Warning: %s not found\n", envFile)
		return
	}

	phpVersion := getEnvValue(envFile, "DEFAULT_PHP_VERSION")
	var phpImage string
	if phpVersion != "" {
		if regexp.MustCompile(`^\d+\.\d+$`).MatchString(phpVersion) {
			phpImage = "php-fpm-" + phpVersion
		} else {
			fmt.Printf("Warning: DEFAULT_PHP_VERSION must be N.N format, got '%s'\n", phpVersion)
		}
	} else {
		fmt.Println("Warning: DEFAULT_PHP_VERSION is not set")
	}

	sqlType := getEnvValue(envFile, "MYSQL_TYPE")
	validSQL := map[string]bool{"mysql": true, "mariadb": true}
	if sqlType != "" && !validSQL[sqlType] {
		fmt.Printf("Warning: MYSQL_TYPE must be 'mysql' or 'mariadb', got '%s'\n", sqlType)
		sqlType = ""
	} else if sqlType == "" {
		fmt.Println("Warning: MYSQL_TYPE is not set")
	}

	wsType := getEnvValue(envFile, "WEB_SERVER")
	validWS := map[string]bool{"nginx": true, "apache": true, "openresty": true, "openlitespeed": true, "litespeed": true}
	if wsType != "" && !validWS[wsType] {
		fmt.Printf("Warning: WEB_SERVER must be 'nginx', 'apache', 'openlitespeed', 'litespeed', or 'openresty', got '%s'\n", wsType)
		wsType = ""
	} else if wsType == "" {
		fmt.Println("Warning: WEB_SERVER is not set")
	}

	var images []string
	if wsType != "" {
		images = append(images, wsType)
	}
	if sqlType != "" {
		images = append(images, sqlType)
	}
	if phpImage != "" {
		images = append(images, phpImage)
	}
	if len(images) == 0 {
		fmt.Println("Warning: No valid images to pull.")
		return
	}

	c.log(fmt.Sprintf("Starting pull for images: %s in background...", strings.Join(images, " ")))
	pullArgs := append([]string{"--context=" + c.Username, "compose", "pull"}, images...)
	cmd := exec.Command("docker", pullArgs...)
	cmd.Dir = fmt.Sprintf("/home/%s/", c.Username)
	nohupOut, _ := os.OpenFile("nohup.out", os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0644)
	nohupErr, _ := os.OpenFile("nohup.err", os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0644)
	cmd.Stdout = nohupOut
	cmd.Stderr = nohupErr
	cmd.Start()
}

// ─────────────────────────────────────────────────────────────
// Start panel service
// ─────────────────────────────────────────────────────────────

func (c *Config) startPanelService() {
	out, _ := runCmd("docker", "--context=default", "compose", "-f", "/root/docker-compose.yml",
		"ps", "--services", "--filter", "status=running")
	if strings.Contains(out, "openpanel") {
		c.log("OpenPanel service is already running.")
		return
	}
	c.log("OpenPanel service is not running. Starting it now...")
	cmd := exec.Command("bash", "-c", "cd /root && docker compose up -d openpanel")
	nohupOut, _ := os.OpenFile("nohup.out", os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0644)
	nohupErr, _ := os.OpenFile("nohup.err", os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0644)
	cmd.Stdout = nohupOut
	cmd.Stderr = nohupErr
	cmd.Start()
	c.log("OpenPanel service has been started in the background.")
}

// ─────────────────────────────────────────────────────────────
// Save user to DB
// ─────────────────────────────────────────────────────────────

func (c *Config) saveUserToDB() {
	c.log("Saving new user to database")
	var query string
	if c.Reseller != "" {
		query = fmt.Sprintf(
			"INSERT INTO users (username, password, owner, email, plan_id, server) VALUES ('%s', '%s', '%s', '%s', '%s', '%s');",
			c.Username, c.HashedPassword, c.Reseller, c.Email, c.PlanID, c.Username,
		)
	} else {
		query = fmt.Sprintf(
			"INSERT INTO users (username, password, email, plan_id, server) VALUES ('%s', '%s', '%s', '%s', '%s');",
			c.Username, c.HashedPassword, c.Email, c.PlanID, c.Username,
		)
	}

	script := fmt.Sprintf(`source %s && mysql --defaults-extra-file="$config_file" -D "$mysql_database" -e %q`, dbConfigFile, query)
	_, err := runCmd("bash", "-c", script)
	if err != nil {
		fmt.Println("[✘] Error: Data insertion failed.")
		c.hardCleanup()
	}
	fmt.Printf("[✔] Successfully added user %s with password: %s\n", c.Username, c.Password)
}

// ─────────────────────────────────────────────────────────────
// Update reseller account count
// ─────────────────────────────────────────────────────────────

func (c *Config) updateAccountsForReseller() {
	if c.Reseller == "" {
		return
	}
	out, err := c.mysqlQuery(fmt.Sprintf("SELECT COUNT(*) FROM users WHERE owner='%s'", c.Reseller))
	if err != nil {
		return
	}
	current := strings.TrimSpace(out)
	c.log(fmt.Sprintf("Updating current accounts count to: %s for reseller: %s", current, c.Reseller))
	resellerLimitsFile := fmt.Sprintf("/etc/openpanel/openadmin/resellers/%s.json", c.Reseller)
	runCmdSilent("bash", "-c", fmt.Sprintf(
		`jq --argjson ca %s '.current_accounts = $ca' %q > /tmp/%s_config.json && mv /tmp/%s_config.json %q`,
		current, resellerLimitsFile, c.Reseller, c.Reseller, resellerLimitsFile,
	))
}

// ─────────────────────────────────────────────────────────────
// Collect stats (initial zero-state file)
// ─────────────────────────────────────────────────────────────

func (c *Config) collectStats() {
	statsFile := fmt.Sprintf("/etc/openpanel/openpanel/core/users/%s/docker_usage.txt", c.Username)
	timestamp := time.Now().Format("2006-01-02-15-04-05")
	data := `{"BlockIO":"0B / 0B","CPUPerc":"0 %","Container":"0","ID":"","MemPerc":"0 %","MemUsage":"0MiB / 0MiB","Name":"","NetIO":"0 / 0","PIDs":0}`
	os.WriteFile(statsFile, []byte(fmt.Sprintf("%s %s\n", timestamp, data)), 0644)
}

// ─────────────────────────────────────────────────────────────
// Send welcome email
// ─────────────────────────────────────────────────────────────

func (c *Config) sendEmailToNewUser() {
	if !c.SendEmail {
		return
	}
	fmt.Printf("Sending email to %s with login information\n", c.Email)
	if !isValidEmail(c.Email) {
		fmt.Printf("%s is not a valid email address. Login information can not be sent to the user.\n", c.Email)
		return
	}

	token := randomBase64(64)
	configFile := panelConfigFile
	runCmdSilent("bash", "-c", fmt.Sprintf(
		`sed -i "s|^mail_security_token=.*$|mail_security_token=%s|" %s`, token, configFile,
	))

	domainOut, _ := runCmd("opencli", "domain")
	domain := strings.TrimSpace(domainOut)
	protocol := "http"
	if regexp.MustCompile(`^[a-zA-Z0-9.\-]+$`).MatchString(domain) {
		if fileExists(fmt.Sprintf("/etc/openpanel/caddy/ssl/acme-v02.api.letsencrypt.org-directory/%s/%s.key", domain, domain)) ||
			fileExists(fmt.Sprintf("/etc/openpanel/caddy/ssl/custom/%s/%s.key", domain, domain)) {
			protocol = "https"
		}
	}

	portOut, _ := runCmd("opencli", "port")
	port := strings.TrimSpace(portOut)
	loginURL := fmt.Sprintf("%s://%s:%s/login", protocol, domain, port)

	runCmd("curl", "-4", "-k", "-X", "POST",
		fmt.Sprintf("%s://%s:2087/send_email", protocol, domain),
		"-F", "transient="+token,
		"-F", "recipient="+c.Email,
		"-F", "subject=New OpenPanel account information",
		"-F", fmt.Sprintf("body=OpenPanel URL: %s | username: %s  | password: %s", loginURL, c.Username, c.Password),
		"--max-time", "15",
	)
}

// ─────────────────────────────────────────────────────────────
// Permissions
// ─────────────────────────────────────────────────────────────

func (c *Config) permissionsDo() {
	runCmdSilent("chown", "-R", c.Username+":"+c.Username, fmt.Sprintf("/home/%s/", c.Username))
}
