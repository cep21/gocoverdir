package main
import (
	"flag"
	"os/exec"
	"time"
	"path/filepath"
	"io/ioutil"
	"os"
	"sync/atomic"
	"fmt"
	"log"
	"path"
	"strings"
	"io"
	"bytes"
)

type gocoverdir struct {
	args args
	ignoreDirSet map[string]struct{}
	storeDir string
	currentOutputIndex int64
	log *log.Logger
	godepEnabled bool

	testOutput io.WriteCloser
}

type args struct {
	covermode string
	cpu int
	ignoreDirs string
	depth int
	timeout time.Duration
	testout string
	logfile string
	coverprofile string
}

var mainStruct gocoverdir

func (m *gocoverdir) setupFlags(fs *flag.FlagSet) {
	fs.StringVar(&m.args.covermode, "covermode", "set", "go test -cover ?")
	fs.IntVar(&m.args.depth, "depth", 10, "Directory depth to search.")
	fs.IntVar(&m.args.cpu, "cpu", -1, "Number of CPUs to use.  If negative, use default.")
	fs.StringVar(&m.args.logfile, "logfile", "-", "Logfile to print output to")
	fs.StringVar(&m.args.testout, "testout", "-", "File to print testing output to")
	fs.StringVar(&m.args.ignoreDirs, "ignoredirs", ".git:Godeps:vendor", "Color separated path of directories to ignore")
	fs.DurationVar(&m.args.timeout, "timeout", time.Second * 3, "Timeout for each individual run of cover")
	fs.StringVar(&m.args.coverprofile, "coverprofile", "coverage.out", "Combined coverage profile file")
}

func (m *gocoverdir) setup() error {
	var err error
	defer func() {
		if err != nil {
			m.log.Printf("Error running setup: %s", err)
		}
	}()

	if m.args.logfile == "-" {
		m.log = log.New(os.Stderr, "", log.LstdFlags)
	} else {
		m.log = log.New(ioutil.Discard, "", 0)
	}

	if m.args.testout == "-" {
		m.testOutput = os.Stdout
	} else {
		if m.testOutput, err = os.Open(m.args.testout); err != nil {
			return err
		}
	}
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
	m.testOutput.Close()
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
	args = append(args, "./" + dirpath)
	cmd := exec.Command(executable, args...)
	cmd.Stdout = m.testOutput
	cmd.Stderr = m.testOutput
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
				err := m.coverDirectory(finalName, depth + 1)
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
}

func main() {
	// handleErr may fatal.  Let the close get called
	defer mainStruct.Close()
	mainStruct.setupFlags(flag.CommandLine)
	flag.Parse()
	err := mainStruct.Main()
	mainStruct.handleErr(err)
}