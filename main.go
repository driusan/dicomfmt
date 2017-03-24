// dicomfmt organizes DICOM folders on the file system into a consistent
// format.

// dicomfmt has two modes of operation: copying files from multiple folders
// into a directory while organizing them, or re-organizing any files that were
// moved in an already "organized" folder by moving them. The former happens
// when you specify multiple directories on the command line (treating the
// last as the target directory to format them into), and the latter if only
// one parameter is supplied (used as both the source and target directory.)
//
// Each series will be organized into the format:
//     targetDir/PatientName/SeriesName/[*].dcm
//
// The name of any series directories that were created will be printed to
// STDOUT.
package main

import (
	"bufio"
	"flag"
	"fmt"
	"io"
	"io/ioutil"
	"log"
	"os"
	"path"
	"path/filepath"
	"unicode"

	"github.com/driusan/go-dicom"
)

var verbose bool

type SeriesInstanceUID string
type FileName string

type SeriesFiles struct {
	PatientName, SeriesDescription string
	Files                          []FileName
}

func (f FileName) String() string {
	return string(f)
}

func isTextFile(file FileName) bool {
	f, err := os.Open(file.String())
	if err != nil {
		log.Println(err)
		return false
	}
	defer f.Close()

	// Check the first 128 runes of the file to see if they're printable
	// characters while interpreted as UTF-8.
	// (Assuming they're all 4 byte long runes, that's still 128*4=512 bytes,
	// which should mean we only need to read 1 disk sector.)
	buffer := bufio.NewReader(f)
	for i := 0; i < 128; i++ {
		r, _, err := buffer.ReadRune()
		if err != nil {
			if verbose {
				log.Println(err)
			}
			return true
		}

		// \n, \t, and \r are control characters, but for our purposes they're printable.
		if !unicode.IsPrint(r) && r != '\n' && r != '\t' && r != '\r' {
			return false
		}
	}
	return true
}

// Removes a directory if the directory is empty.
func removeEmpty(dir string) bool {
	files, err := ioutil.ReadDir(dir)
	if err != nil {
		return false
	}
	if len(files) == 0 {
		err := os.Remove(dir)
		return err == nil
	}
	return false
}

// Split series takes a path name as a parameter, and map of the files contained
// in each SeriesInstanceUID in the directory. It will recursively parse
// files subdirectories of the directory that it's parsing.
func SplitSeries(dir FileName) (map[SeriesInstanceUID]SeriesFiles, error) {
	if dir == "" {
		return nil, fmt.Errorf("Must provide a directory to split.")
	}

	files, err := ioutil.ReadDir(dir.String())
	if err != nil {
		return nil, err
	}

	series := make(map[SeriesInstanceUID]SeriesFiles)
	for _, file := range files {
		filename := FileName(filepath.Clean(dir.String() + "/" + file.Name()))

		if file.IsDir() {
			// Recursively add any subdirectories as documented.
			subdirFiles, err := SplitSeries(filename)
			if err != nil {
				log.Println(err)
				continue
			}
			for newSeries, seriesData := range subdirFiles {
				oldseries, ok := series[newSeries]
				if ok {
					// The series already existed, so just
					// add the new files to it.
					oldseries.Files = append(oldseries.Files, seriesData.Files...)
					series[newSeries] = oldseries
				} else {
					// It's a new series, so set the key
					series[newSeries] = seriesData
				}
			}
		} else {
			if isTextFile(filename) {
				if verbose {
					log.Printf("Skipping %s: not a DICOM file.\n", file.Name())
				}
				continue
			}

			bytes, err := ioutil.ReadFile(filename.String())
			if err != nil {
				log.Println(err)
				continue
			}

			parser, err := dicom.NewParser()
			if err != nil {
				log.Fatalln(err)
			}
			data, err := parser.Parse(bytes)
			if err != nil {
				log.Println(filename, " parser error: ", err)
				continue
			}

			newSeriesEl, err := data.LookupElement("SeriesInstanceUID")
			if err != nil {
				log.Println(filename, " lookup error", err)
				continue
			}
			newSeries := SeriesInstanceUID(newSeriesEl.GetValue())
			if newSeries == "" {
				log.Println("Could not find SeriesInstanceUID")
				continue
			}
			oldseries, ok := series[newSeries]
			if ok {
				oldseries.Files = append(oldseries.Files, filename)
				series[newSeries] = oldseries
			} else {
				patient, err := data.LookupElement("PatientName")
				if err != nil {
					log.Println(filename, " lookup error for PatientName", err)
					continue
				}
				sd, err := data.LookupElement("SeriesDescription")
				if err != nil {
					log.Println(filename, " lookup error for SeriesDescription", err)
					continue
				}

				series[newSeries] = SeriesFiles{
					PatientName:       patient.GetValue(),
					SeriesDescription: sd.GetValue(),
					Files:             []FileName{filename},
				}
			}
		}
	}
	return series, nil
}

type fileAction func(src, dst FileName) error

func moveFile(src, dst FileName) error {
	return os.Rename(src.String(), dst.String())
}

func copyFile(src, dst FileName) error {
	f, err := os.Open(src.String())
	if err != nil {
		return err
	}
	defer f.Close()
	fdst, err := os.Create(dst.String())
	if err != nil {
		return err
	}
	defer fdst.Close()
	_, err = io.Copy(fdst, f)
	return err
}

func main() {
	var mv bool

	flag.BoolVar(&verbose, "verbose", false, "Print extra information to standard error.")
	if len(os.Args) < 2 {
		fmt.Fprintf(os.Stderr, "Usage: %s [options] source_dir [...] target_directory\n\n", os.Args[0])
		flag.PrintDefaults()
		os.Exit(1)
	}

	flag.Parse()
	args := flag.Args()

	var srcDirs []string
	var dst string
	switch len(args) {
	case 1:
		srcDirs = args
		dst = args[0]
		mv = true
	default:
		srcDirs = args[:len(args)-1]
		dst = args[len(args)-1]
	}

	// Ensure that the dst directory exists, and create it if not.
	if _, err := os.Stat(dst); os.IsNotExist(err) {
		if err := os.MkdirAll(dst, 0750); err != nil {
			log.Fatalln(err)
		}
	}

	// Ensure each sourceDir exists before doing anything.
	for _, src := range srcDirs {
		_, err := os.Stat(src)
		if os.IsNotExist(err) {
			log.Printf("%s does not exist.", src)
			continue
		}
		series, err := SplitSeries(FileName(src))
		if err != nil {
			log.Println(err)
			continue
		}
		for _, files := range series {
			var movedSome bool
			dstDir := fmt.Sprintf("%s/%s/%s", dst, files.PatientName, files.SeriesDescription)
			for _, file := range files.Files {
				dstFile := FileName(filepath.Clean(dstDir + "/" + path.Base(file.String())))

				if dstFile == file {
					continue
				}
				movedSome = true
				// If there's an error it's likely because we ran
				// out of diskspace or don't have permission,
				// so treat it as fatal instead of trying to continue.
				// on to the next series.
				if err := os.MkdirAll(dstDir, 0750); err != nil {
					log.Fatalln(err)
				}

				var action fileAction
				if mv {
					action = moveFile
				} else {
					action = copyFile
				}
				if err := action(file, dstFile); err != nil {
					log.Fatalln(err)
				}

				// This isn't very efficient, but we need
				// to remove empty directories after moving
				// all the files out of it.
				if mv {
					srcDir := filepath.Dir(file.String())
					if removed := removeEmpty(srcDir); removed {
						// The scan dir was removed,
						// remove the patientname dir
						// if it was the last scan.
						parentDir := filepath.Dir(srcDir)
						removeEmpty(parentDir)
					}
				}
			}

			if movedSome {
				fmt.Println(filepath.Clean(dstDir))
			}
		}
	}
}
