package main

import (
	"bytes"
	"flag"
	"fmt"
	"io"
	"io/ioutil"
	"log"
	"os"
	"os/exec"
	"path"
	"path/filepath"
	"strings"
	"sync/atomic"
	"time"

	"golang.org/x/tools/cover"
)

type gocoverdir struct {
	args               args
	ignoreDirSet       map[string]struct{}
	storeDir           string
	currentOutputIndex int64
	log                *log.Logger
	godepEnabled       bool

	panicPrintBuffer bytes.Buffer
	logfile io.WriteCloser
	testOutputStderr io.Writer
	testOutputStdout io.Writer
}

type args struct {
	covermode        string
	cpu              int
	ignoreDirs       string
	depth            int
	timeout          time.Duration
	logfile          string
	coverprofile     string
	printcoverage    bool
	requiredcoverage float64
	race bool

	htmlcoverage bool
}

var mainStruct gocoverdir

func (m *gocoverdir) setupFlags(fs *flag.FlagSet) {
	fs.StringVar(&m.args.covermode, "covermode", "set", "Same as -covermode in 'go test'")
	fs.IntVar(&m.args.cpu, "cpu", -1, "Same as -cpu in 'go test'")
	fs.BoolVar(&m.args.race, "race", false, "Same as -race in 'go test'")
	fs.DurationVar(&m.args.timeout, "timeout", time.Second*3, "Same as -timeout in 'go test'")
	fs.StringVar(&m.args.coverprofile, "coverprofile", filepath.Join(os.TempDir(), "coverage.out"), "Same as -coverprofile in 'go test', but will be a combined cover profile.")

	fs.IntVar(&m.args.depth, "depth", 10, "Directory depth to search.")
	fs.StringVar(&m.args.ignoreDirs, "ignoredirs", ".git:Godeps:vendor", "Color separated path of directories to ignore")

	fs.StringVar(&m.args.logfile, "logfile", "-", "Logfile to print debug output to.  Empty means be silent unless there is an error, then dump to stderr")

	fs.BoolVar(&m.args.printcoverage, "printcoverage", false, "Print coverage amount to stdout")
	fs.Float64Var(&m.args.requiredcoverage, "requiredcoverage", 0.0, "Program will fatal if coverage is < this value")
	fs.BoolVar(&m.args.htmlcoverage, "htmlcoverage", false, "If true, will generate coverage output in a temp file")
}

func (m *gocoverdir) setupLogFile() error {
	if m.args.logfile == "-" {
		m.log = log.New(os.Stderr, "", log.LstdFlags)
		m.testOutputStderr = os.Stderr
		m.testOutputStdout = os.Stdout
	} else if m.args.logfile == "" {
		m.log = log.New(&m.panicPrintBuffer, "", 0)
		m.testOutputStderr = &m.panicPrintBuffer
		m.testOutputStdout = &m.panicPrintBuffer
	} else {
		var err error
		m.logfile, err = os.OpenFile(m.args.logfile, os.O_CREATE | os.O_WRONLY, 0644)
		if err != nil {
			return err
		}
		m.log = log.New(m.logfile, "", 0)
		m.testOutputStderr = m.logfile
		m.testOutputStdout = m.logfile
	}
	return nil
}

func (m *gocoverdir) verifyParams() {
	if m.args.requiredcoverage < 0.0 || m.args.requiredcoverage > 100.0001 {
		m.log.Panicf("Required coverage must be >= 0 && <= 100, but is %f", m.args.requiredcoverage)
	}
}

func (m *gocoverdir) setup() error {
	var err error
	defer func() {
		if err != nil {
			m.log.Printf("Error running setup: %s", err)
		}
	}()
	m.setupLogFile()
	m.verifyParams()

	if f, err := os.Open("Godeps"); err == nil {
		if stat, err := f.Stat(); err == nil && stat.IsDir() {
			m.godepEnabled = true
		}
	}

	m.storeDir, err = ioutil.TempDir("", "gocoverdir")
	if err != nil {
		return err
	}
	m.log.Printf("coverdir %s", m.storeDir)
	ignoreDirs := filepath.SplitList(m.args.ignoreDirs)
	m.ignoreDirSet = make(map[string]struct{}, len(ignoreDirs))
	for _, s := range ignoreDirs {
		m.ignoreDirSet[s] = struct{}{}
	}
	m.log.Printf("Setup done")
	return nil
}

func (m *gocoverdir) Close() error {
	if len(m.storeDir) < 4 {
		panic("mainStruct not setup correctly")
	}
	if m.logfile != nil {
		m.logfile.Close()
	}
	return os.RemoveAll(m.storeDir)
}

func (m *gocoverdir) nextCoverprofileName() string {
	return fmt.Sprintf("gocoverdirprofile%d.cover", atomic.AddInt64(&m.currentOutputIndex, 1))
}

func (m *gocoverdir) coverDir(dirpath string) error {
	args := []string{}
	var executable string
	if m.godepEnabled {
		args = append(args, "go")
		executable = "godep"
	} else {
		executable = "go"
	}
	args = append(args, "test", "-cover", "-covermode", m.args.covermode, "-coverprofile", m.nextCoverprofileName(), "-outputdir", m.storeDir)
	if m.args.timeout.Nanoseconds() > 0 {
		args = append(args, "-timeout", m.args.timeout.String())
	}
	if m.args.cpu >= 0 {
		args = append(args, "-cpu", fmt.Sprintf("%d", m.args.cpu))
	}
	if m.args.race {
		args = append(args, "-race")
	}
	args = append(args, "./"+dirpath)
	cmd := exec.Command(executable, args...)
	cmd.Stdout = m.testOutputStdout
	cmd.Stderr = m.testOutputStderr
	m.log.Printf("Executing %s %s", cmd.Path, strings.Join(cmd.Args, " "))
	if err := cmd.Start(); err != nil {
		return err
	}
	err := cmd.Wait()
	return err
}

func (m *gocoverdir) coverDirectory(dirpath string, depth int) error {
	m.log.Printf("Coverdir on %s", dirpath)
	if depth > m.args.depth {
		return nil
	}
	files, err := ioutil.ReadDir(dirpath)
	if err != nil {
		return err
	}
	if m.containsGoTest(files) {
		m.log.Printf("Go files in directory")
		coverRes := m.coverDir(dirpath)
		if coverRes != nil {
			return coverRes
		}
	}
	for _, file := range files {
		if file.IsDir() {
			if _, ignoredDir := m.ignoreDirSet[file.Name()]; !ignoredDir {
				finalName := filepath.Join(dirpath, file.Name())
				err := m.coverDirectory(finalName, depth+1)
				if err != nil {
					return err
				}
			}
		}
	}
	return nil
}

func (m *gocoverdir) containsGoTest(files []os.FileInfo) bool {
	for _, file := range files {
		if path.Ext(file.Name()) == ".go" {
			return true
		}
	}
	return false
}

func (m *gocoverdir) Main() error {
	if err := m.setup(); err != nil {
		return err
	}
	return m.coverDirectory(".", 0)
}

func (m *gocoverdir) handleErr(err error) {
	defer func() {
		if err != nil {
			// Panic, rather than fatal, lets the defer Close() happen
			m.log.Panic(err.Error())
		}
	}()
	if err != nil {
		return
	}

	files, err := ioutil.ReadDir(m.storeDir)
	if err != nil {
		return
	}
	outputBuffer := bytes.Buffer{}
	for _, file := range files {
		if !file.IsDir() {
			fileContents, err := ioutil.ReadFile(filepath.Join(m.storeDir, file.Name()))
			if err != nil {
				return
			}
			if outputBuffer.Len() == 0 {
				outputBuffer.Write(fileContents)
				continue
			}
			fileLines := strings.Split(string(fileContents), "\n")
			outputBuffer.WriteString(strings.Join(fileLines[1:], "\n"))
		}
	}
	err = ioutil.WriteFile(m.args.coverprofile, outputBuffer.Bytes(), 0644)
	if err != nil {
		return
	}
	err = m.handleCoverage()
}

func (m *gocoverdir) handleCoverage() error {
	var err error
	if m.args.htmlcoverage {
		htmlout := filepath.Join(os.TempDir(), "cover.html")
		m.log.Printf("Generating coverage HTML at %s or %s", htmlout, "file://"+htmlout)
		cmd := exec.Command("go", "tool", "cover", "-html", m.args.coverprofile, "-o", htmlout)
		if err = cmd.Run(); err != nil {
			return err
		}
	}

	if m.args.printcoverage || m.args.requiredcoverage > 0.0 {
		var coverage float64
		coverage, err = m.calculateCoverage()
		if err != nil {
			return err
		}

		if m.args.printcoverage {
			fmt.Printf("coverage: %.1f%% of statements\n", coverage)
		}
		if m.args.requiredcoverage > 0.0 {
			if coverage < m.args.requiredcoverage-.001 {
				msg := fmt.Sprintf("Code coverage %f less than required %f.  See profile.out to debug or run 'go tool cover -html %s -o /tmp/cover.html'", coverage, m.args.requiredcoverage, m.args.coverprofile)
				m.log.Panic(msg)
				panic(msg)
			}
		}
	}
	return nil
}

func (m *gocoverdir) calculateCoverage() (float64, error) {
	profiles, err := cover.ParseProfiles(m.args.coverprofile)
	if err != nil {
		return 0.0, err
	}
	total := 0
	covered := 0
	for _, profile := range profiles {
		for _, block := range profile.Blocks {
			total += block.NumStmt
			if block.Count > 0 {
				covered += block.NumStmt
			}
		}
	}
	if total == 0 {
		return 0.0, nil
	}
	return float64(covered) / float64(total) * 100, nil
}

func main() {
	// handleErr may fatal.  Let the close get called
	defer mainStruct.Close()
	defer func() {
		if panicCondition := recover(); panicCondition != nil {
			io.Copy(os.Stderr, &mainStruct.panicPrintBuffer)
			panic(panicCondition)
		}
	}()
	mainStruct.setupFlags(flag.CommandLine)
	flag.Parse()
	err := mainStruct.Main()
	mainStruct.handleErr(err)
}
