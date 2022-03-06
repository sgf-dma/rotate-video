
package main

import (
    "fmt"
    "os"
    "path/filepath"
    "io/fs"
    "os/exec"
    "strings"
    "errors"
    "bufio"
    "io"
    "log"
    "runtime"

    "github.com/mitchellh/cli"

    "github.com/jessevdk/go-flags"
    "encoding/json"
)

type streamType struct {
    Index int
    Codec_name string
    Codec_type string
}

type containerType struct {
    Streams []streamType
}

var opts struct {
    In flags.Filename `short:"i" description:"Input filename" value-name:"FILE" required:"true"`
    Vf string `long:"vf" description:"FFmpeg video filter"`
}

var rootPath string
var ffmpegUserArgs []string
var ffmpegCodecArgs = map[string][]string {
    "h264": { "-c:v", "libx264", "-preset", "ultrafast", "-crf", "30"},
    "default": { "-c:v", "libx264", "-preset", "ultrafast", "-crf", "30"},
}

var ffmpegPath string
var ffprobePath string

var logFlags = log.Ltime | log.Lmsgprefix

func ffprobe(inPath string) (*containerType, error) {
    var ct containerType
    l := log.New(os.Stderr, "ffprobe(): ", logFlags)

    args := []string{"-loglevel", "quiet", "-show_streams", "-select_streams", "v", "-print_format", "json", inPath}
    ffmpeg := exec.Command(ffprobePath, args...)
    stdout, err := ffmpeg.CombinedOutput()
    if err != nil {
        l.Printf("ffprobe does not recognize file '%v' (%v)\n", inPath, err)
        return nil, err
    }

    json.Unmarshal(stdout, &ct)
    l.Printf("ffprobe %#v\n", ct)
    return &ct, nil
}

func readBuf (r *bufio.Reader, w *os.File) {
    for {
        str, err := r.ReadBytes('\n')
        w.Write(str)
        if err != nil {
            if err == io.EOF {
                //fmt.Println("EOF")
                break
            }
            fmt.Printf("reading err: %v\n", err)
            return
        }
    }
    return
}

func convertFile(inPath string, outPath string) error {
    l := log.New(os.Stderr, "convertFile(): ", logFlags)

    ct, err := ffprobe(inPath)
    if err != nil {
        return nil
    }

    var ffArgs []string
    if len(ffmpegUserArgs) == 0 {
        if _, ok := ffmpegCodecArgs[ct.Streams[0].Codec_name]; !ok {
            ffArgs = ffmpegCodecArgs["default"]
        } else {
            ffArgs = ffmpegCodecArgs[ct.Streams[0].Codec_name]
        }
    } else {
        ffArgs = ffmpegUserArgs
    }

    args := make([]string, 0)
    args = append(args, "-stats", "-n", "-i", inPath)
    if len(opts.Vf) != 0 {
        args = append(args, "-vf", opts.Vf)
    }
    args = append(args, ffArgs...)
    args = append(args, outPath)

    l.Printf("Calling %v\n", args)
    ffmpeg := exec.Command(ffmpegPath, args...)
    //stdout, err := ffmpeg.CombinedOutput()
    stdout, err := ffmpeg.StdoutPipe()
    if err != nil {
        l.Println(err)
        return nil
    }

    stderr, err := ffmpeg.StderrPipe()
    if err != nil {
        l.Println(err)
        return nil
    }

    bout := bufio.NewReader(stdout)
    berr := bufio.NewReader(stderr)
    if err = ffmpeg.Start(); err != nil {
        l.Printf("ffmpeg exited with error = %v, skip\n", err)
        os.Remove(outPath)
        return nil
    }

    go readBuf(bout, os.Stdout)
    go readBuf(berr, os.Stderr)

    if err := ffmpeg.Wait(); err != nil {
        l.Printf("ffmpeg exited with error = %v, skip\n", err)
        os.Remove(outPath)
        return nil
    }
    //fmt.Printf("Result: %v\n", string(stdout))
    return nil
}

const (
    rotatedFile = iota
    rotatedDir
)

func walkFiles(path string, d fs.DirEntry, err error) error {
    l := log.New(os.Stderr, "walkFiles(): ", logFlags)

    l.Printf("Considering '%v'\n", path)
    if path == rootPath {
        return nil
    }

    if path != rootPath && d.IsDir() {
        l.Printf("Ignore sub-directory '%v'\n", path)
        return fs.SkipDir
    }

    ext := filepath.Ext(path)
    s := strings.TrimSuffix(strings.TrimSuffix(path, ext), "-rotated")
    outPath := s + "-rotated" + ext
    if path == outPath {
        l.Printf("File '%v' is rotation result, skip\n", path)
        return nil
    }

    _, err = os.Stat(outPath)
    if !errors.Is(err, os.ErrNotExist) {
        if err != nil {
            l.Println(err)
            return err
        } else {
            l.Printf("File '%v' is already rotated, skip\n", path)
            return nil
        }
    }

    return convertFile(path, outPath)
}

type FooCommand struct {}

func (cmd FooCommand) Help() string {
    return "foo command help"
}

func (cmd FooCommand) Synopsis() string {
    return "foo synopsis"
}

func (cmd FooCommand) Run(args []string) int {
    fmt.Printf("foo command args: %v\n", args)
    return 0
}

func fooCommandFactory() (cmd cli.Command, err error) {
    var fooCmd FooCommand
    return fooCmd, nil
}

func lookupBin(bin string) (path string, err error) {
    l := log.New(os.Stderr, "lookupBin(): ", logFlags)
    addPathes := []string{".", filepath.Join(".", "bin")}

    l.Printf("Looking up path for %v\n", bin)
    path, err = exec.LookPath(bin)
    if err != nil {
        l.Printf("Not found in $PATH, trying other pathes..\n")
        for _, p := range addPathes {
            path = filepath.Join(p, bin)
            fi, err := os.Stat(path)
            if err != nil {
                continue
            }
            if fi.Mode() & 0111 == 0 {
                l.Printf("Found %v, but it's not executable\n", path)
                continue
            }
            l.Printf("Found %v\n", path)
            return path, nil
        }
        if errors.Is(err, exec.ErrNotFound) {
            err = fmt.Errorf("'%v' found neither in $PATH or in additional pathes %v\n", bin, addPathes)
        }
    } else {
        l.Printf("Found %v\n", path)
    }
    return
}

func main() {
    l := log.New(os.Stderr, "main(): ", logFlags)
    //flags.NewParser(, flags.Default)

    optP := flags.NewParser(&opts, flags.Default | flags.IgnoreUnknown)
    args, err := optP.Parse()
    if err != nil {
        switch flagsErr := err.(type) {
            case flags.ErrorType:
                if flagsErr == flags.ErrHelp {
                    os.Exit(0)
                }
                os.Exit(1)
            default:
                os.Exit(1)
        }
    }
    l.Printf("found options: %v\n", opts)
    ffmpegUserArgs = args

    rootPath = string(opts.In)
    fi, err := os.Stat(rootPath)
    if err != nil {
        fmt.Println(err)
        os.Exit(1)
    }

    if runtime.GOOS == "windows" {
        ffmpegPath, err = lookupBin("ffmpeg.exe")
        if err != nil {
            log.Fatal(err)
        }
        ffprobePath, err = lookupBin("ffprobe.exe")
        if err != nil {
            log.Fatal(err)
        }
    } else {
        ffmpegPath, err = lookupBin("ffmpeg")
        if err != nil {
            log.Fatal(err)
        }
        ffprobePath, err = lookupBin("ffprobe")
        if err != nil {
            log.Fatal(err)
        }
    }

    l.Printf("Found ffmpeg %v and ffprobe %v\n", ffmpegPath, ffprobePath)
    l.Printf("ffmpeg extra arguments: %v\n", ffmpegUserArgs)
    if fi.IsDir() {
        l.Printf("Rotate all files in directory: %v\n", rootPath)
        filepath.WalkDir(rootPath, walkFiles)
    } else {
        l.Printf("Run command for SINGLE file %v\n", rootPath)
    }

    /*
        c := cli.NewCLI("app", "1.0.0")
        c.Args = os.Args[1:]
        c.Commands = map[string]cli.CommandFactory{
                "foo": fooCommandFactory,
                //"bar": barCommandFactory,
        }

        exitStatus, err := c.Run()
        if err != nil {
                fmt.Println(err)
        }

        os.Exit(exitStatus)
        */
}

