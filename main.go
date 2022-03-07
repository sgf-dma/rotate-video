
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

    "github.com/jessevdk/go-flags"
    "github.com/fatih/color"
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
    Vf string `long:"vf" description:"FFmpeg -vf video filter string" value-name:"STRING"`
    RotateIn rotatePlace `long:"rotate-in" description:"Where to place rotated files." choice:"dir" choice:"here" default:"dir"`
}

var rootPath string
var ffmpegUserArgs []string
var ffmpegCodecArgs = map[string][]string {
    "h264": { "-c:v", "libx264", "-preset", "ultrafast", "-crf", "30"},
    "default": { "-c:v", "libx264", "-preset", "ultrafast", "-crf", "30"},
}

type rotatePlace int
const (
    rotateHere rotatePlace = iota
    rotateInDir
)
var rotatedSuffix = "rotated"

func (r *rotatePlace) MarshalFlag() (flag string, err error) {
    switch *r {
    case rotateHere:
        flag = "here"
    case rotateInDir:
        flag = "dir"
    default:
        err = fmt.Errorf("Incorrect rotate value '%v'\n", *r)
    }
    return
}

func (r *rotatePlace) UnmarshalFlag(v string) (err error) {
    switch v {
    case "here":
        *r = rotateHere
    case "dir":
        *r = rotateInDir
    default:
        err = fmt.Errorf("Unknown rotate flag value '%v'\n", v)
    }
    return
}

var ffmpegPath string
var ffprobePath string

var logFlags = log.Ltime | log.Lmsgprefix

func debug(l *log.Logger, format string, a ...interface{}) {
    color.Set(color.FgGreen)
    l.Printf(format, a...)
    color.Unset()
}

func info(l *log.Logger, format string, a ...interface{}) {
    color.Set(color.FgCyan)
    l.Printf(format, a...)
    color.Unset()
}

func crit(l *log.Logger, format string, a ...interface{}) {
    color.Set(color.FgRed)
    l.Printf(format, a...)
    color.Unset()
    os.Exit(1)
}

func critln(l *log.Logger, a ...interface{}) {
    color.Set(color.FgRed)
    l.Println(a...)
    color.Unset()
    os.Exit(1)
}

func ffprobe(inPath string) (*containerType, error) {
    var ct containerType
    l := log.New(os.Stderr, "ffprobe(): ", logFlags)

    args := []string{"-loglevel", "quiet", "-show_streams", "-select_streams", "v", "-print_format", "json", inPath}
    ffmpeg := exec.Command(ffprobePath, args...)
    stdout, err := ffmpeg.CombinedOutput()
    if err != nil {
        info(l, "ffprobe does not recognize file '%v' (%v)\n", inPath, err)
        return nil, err
    }

    json.Unmarshal(stdout, &ct)
    debug(l, "ffprobe %#v\n", ct)
    return &ct, nil
}

func convertTo(inPath string, outPath string) error {
    l := log.New(os.Stderr, "convertTo(): ", logFlags)

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

    debug(l, "Calling ffmpeg with args: %v\n", args)
    ffmpeg := exec.Command(ffmpegPath, args...)
    //stdout, err := ffmpeg.CombinedOutput()
    stdout, err := ffmpeg.StdoutPipe()
    if err != nil {
        critln(l, err)
        return err
    }

    stderr, err := ffmpeg.StderrPipe()
    if err != nil {
        critln(l, err)
        return err
    }

    bout := bufio.NewReader(stdout)
    berr := bufio.NewReader(stderr)
    if err = ffmpeg.Start(); err != nil {
        info(l, "ffmpeg exited with error = %v, skip\n", err)
        //os.Remove(outPath)
        return nil
    }

    readBuf := func (r *bufio.Reader, w *os.File) {
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

    go readBuf(bout, os.Stdout)
    go readBuf(berr, os.Stderr)

    if err := ffmpeg.Wait(); err != nil {
        info(l, "ffmpeg exited with error = %v, skip\n", err)
        //os.Remove(outPath)
        return nil
    }
    //fmt.Printf("Result: %v\n", string(stdout))
    return nil
}

// Argument is assumed to be a file!
func convertFile(path string) (err error) {
    l := log.New(os.Stderr, "convertFile(): ", logFlags)
    var outPath string

    switch opts.RotateIn {
    case rotateHere:
        ext := filepath.Ext(path)
        s := strings.TrimSuffix(strings.TrimSuffix(path, ext), "-" + rotatedSuffix)
        outPath = s + "-" + rotatedSuffix + ext
        if path == outPath {
            info(l, "File '%v' is rotation result, skip\n", path)
            return nil
        }
    case rotateInDir:
        p := filepath.Join(filepath.Dir(path), rotatedSuffix)
        err = os.Mkdir(p,  0770)
        if err != nil && !errors.Is(err, fs.ErrExist) {
            critln(l, err)
            return err
        }
        outPath = filepath.Join(p, filepath.Base(path))
    default:
        critln(l, fmt.Errorf("Unknown rotate place '%v'\n", opts.RotateIn))
    }

    _, err = os.Stat(outPath)
    if !errors.Is(err, os.ErrNotExist) {
        if err != nil {
            critln(l, err)
            return err
        } else {
            info(l, "File '%v' is already rotated, skip\n", path)
            return nil
        }
    }

    return convertTo(path, outPath)
}

func walkFiles(path string, d fs.DirEntry, err error) error {
    l := log.New(os.Stderr, "walkFiles(): ", logFlags)

    debug(l, "Considering '%v'\n", path)
    if path == rootPath {
        return nil
    }

    if path != rootPath && d.IsDir() {
        debug(l, "Ignore sub-directory '%v'\n", path)
        return fs.SkipDir
    }

    return convertFile(path)
}

func lookupBin(bin string) (path string, err error) {
    l := log.New(os.Stderr, "lookupBin(): ", logFlags)
    addPathes := []string{".", filepath.Join(".", "bin")}

    debug(l, "Looking up path for %v\n", bin)
    path, err = exec.LookPath(bin)
    if err != nil {
        debug(l, "Not found in $PATH, trying other pathes..\n")
        for _, p := range addPathes {
            path = filepath.Join(p, bin)
            fi, err := os.Stat(path)
            if err != nil {
                continue
            }
            if runtime.GOOS != "windows" && fi.Mode() & 0111 == 0 {
                debug(l, "Found %v, but it's not executable (%v)\n", path, fi.Mode())
                continue
            }
            debug(l, "Found %v\n", path)
            return path, nil
        }
        if errors.Is(err, exec.ErrNotFound) {
            err = fmt.Errorf("'%v' found neither in $PATH or in additional pathes %v\n", bin, addPathes)
        }
    } else {
        debug(l, "Found %v\n", path)
    }
    return
}

func main() {
    l := log.New(os.Stderr, "main(): ", logFlags)

    optP := flags.NewParser(&opts, flags.Default | flags.IgnoreUnknown)
    args, err := optP.Parse()
    if err != nil {
        switch flagsErr := err.(type) {
            case flags.ErrorType:
                if flagsErr == flags.ErrHelp {
                    os.Exit(0)
                }
                //optP.WriteHelp(os.Stderr)
                os.Exit(1)
            default:
                //optP.WriteHelp(os.Stderr)
                os.Exit(1)
        }
    }
    debug(l, "found options: %+v\n", opts)
    ffmpegUserArgs = args

    rootPath = string(opts.In)
    fi, err := os.Stat(rootPath)
    if err != nil {
        critln(l, err)
        os.Exit(1)
    }

    if runtime.GOOS == "windows" {
        ffmpegPath, err = lookupBin("ffmpeg.exe")
        if err != nil {
            critln(l, err)
        }
        ffprobePath, err = lookupBin("ffprobe.exe")
        if err != nil {
            critln(l, err)
        }
    } else {
        ffmpegPath, err = lookupBin("ffmpeg")
        if err != nil {
            critln(l, err)
        }
        ffprobePath, err = lookupBin("ffprobe")
        if err != nil {
            critln(l, err)
        }
    }

    debug(l, "Found ffmpeg %v and ffprobe %v\n", ffmpegPath, ffprobePath)
    debug(l, "ffmpeg extra arguments: %v\n", ffmpegUserArgs)
    if fi.IsDir() {
        debug(l, "Rotate all files in directory: %v\n", rootPath)
        filepath.WalkDir(rootPath, walkFiles)
    } else {
        debug(l, "Run command for SINGLE file %v\n", rootPath)
        convertFile(rootPath)
    }
}

