
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

func ffprobe(inPath string) (*containerType, error) {
    var ct containerType

    args := []string{"-loglevel", "quiet", "-show_streams", "-select_streams", "v", "-print_format", "json", inPath}
    ffmpeg := exec.Command("ffprobe", args...)
    stdout, err := ffmpeg.CombinedOutput()
    if err != nil {
        fmt.Printf("ffprobe does not recognize file '%v' (%v)\n", inPath, err)
        return nil, err
    }

    json.Unmarshal(stdout, &ct)
    fmt.Printf("ffprobe %#v\n", ct)
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

    fmt.Printf("Calling %v\n", args)
    ffmpeg := exec.Command("ffmpeg", args...)
    //stdout, err := ffmpeg.CombinedOutput()
    stdout, err := ffmpeg.StdoutPipe()
    if err != nil {
        fmt.Println(err)
        return nil
    }

    stderr, err := ffmpeg.StderrPipe()
    if err != nil {
        fmt.Println(err)
        return nil
    }

    bout := bufio.NewReader(stdout)
    berr := bufio.NewReader(stderr)
    if err = ffmpeg.Start(); err != nil {
        fmt.Printf("ffmpeg exited with error = %v, skip\n", err)
        os.Remove(outPath)
        return nil
    }

    go readBuf(bout, os.Stdout)
    go readBuf(berr, os.Stderr)

    if err := ffmpeg.Wait(); err != nil {
        fmt.Printf("ffmpeg exited with error = %v, skip\n", err)
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
    fmt.Printf("Considering '%v'\n", path)
    if path == rootPath {
        return nil
    }

    if path != rootPath && d.IsDir() {
        fmt.Printf("Ignore sub-directory '%v'\n", path)
        return fs.SkipDir
    }

    ext := filepath.Ext(path)
    s := strings.TrimSuffix(strings.TrimSuffix(path, ext), "-rotated")
    outPath := s + "-rotated" + ext
    if path == outPath {
        fmt.Printf("File '%v' is rotation result, skip\n", path)
        return nil
    }

    _, err = os.Stat(outPath)
    if !errors.Is(err, os.ErrNotExist) {
        if err != nil {
            fmt.Println(err)
            return err
        } else {
            fmt.Printf("File '%v' is already rotated, skip\n", path)
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

func main() {
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
    fmt.Printf("found options: %v\n", opts)
    ffmpegUserArgs = args

    rootPath = string(opts.In)
    fi, err := os.Stat(rootPath)
    if err != nil {
        fmt.Println(err)
        os.Exit(1)
    }

    fmt.Printf("ffmpeg extra arguments: %v\n", ffmpegUserArgs)
    if fi.IsDir() {
        fmt.Printf("Rotate all files in directory: %v\n", rootPath)
        filepath.WalkDir(rootPath, walkFiles)
    } else {
        fmt.Printf("Run command for SINGLE file %v\n", rootPath)
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

