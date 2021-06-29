package main

import (
	"archive/tar"
	"compress/gzip"
	"context"
	"fmt"
	"io"
	"io/ioutil"
	"log"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"syscall"
	"time"

	ocilayout "github.com/containers/image/v5/oci/layout"
	"github.com/containers/image/v5/types"
	"github.com/fxamacker/cbor/v2"
	"github.com/spf13/cobra"
	"github.com/zeebo/blake3"
)

type OCIX struct {
	Version uint            `cbor:"version"`
	Files   map[string]File `cbor:"files"`
}

type File struct {
	Mode        Mode                    `cbor:"mode"`
	Uid         *uint                   `cbor:"uid,omitempty"`
	Gid         *uint                   `cbor:"gid,omitempty"`
	Username    *string                 `cbor:"username,omitempty"`
	Groupname   *string                 `cbor:"groupname,omitempty"`
	Atime       *time.Time              `cbor:"atime,omitempty"`
	Btime       *time.Time              `cbor:"btime,omitempty"`
	Ctime       *time.Time              `cbor:"ctime,omitempty"`
	Mtime       *time.Time              `cbor:"mtime,omitempty"`
	Xattr       *map[string]interface{} `cbor:"xattr,omitempty"`
	Type        string                  `cbor:"type"`
	Regularfile *Regularfile            `cbor:"regularfile,omitempty"`
	Link        *Link                   `cbor:"link,omitempty"`
	Directory   *Directory              `cbor:"directory,omitempty"`
	SymLink     *SymLink                `cbor:"symlink,omitempty"`
	Character   *Character              `cbor:"character,omitempty"`
	Block       *Block                  `cbor:"block,omitempty"`
	Fifo        *Fifo                   `cbor:"fifo,omitempty"`
}

type Mode struct {
	_      struct{} `cbor:",toarray"`
	User   RWX      `cbor:"user"`
	Group  RWX      `cbor:"group"`
	Other  RWX      `cbor:"other"`
	Setuid bool     `cbor:"setuid"`
	Setgid bool     `cbor:"setgid"`
	Sticky bool     `cbor:"sticky"`
}
type RWX struct {
	_       struct{} `cbor:",toarray"`
	Read    bool     `cbor:"read"`
	Write   bool     `cbor:"write"`
	Execute bool     `cbor:"execute"`
}

type Regularfile struct {
	_          struct{} `cbor:",toarray"`
	Size       uint64   `cbor:"size"`
	Blake3Hash []byte   `cbor:"b3-256"`
}

type Directory struct {
	_ struct{} `cbor:",toarray"`
}

type Link struct {
	_      struct{} `cbor:",toarray"`
	Target string   `cbor:"target"`
}

type SymLink struct {
	_      struct{} `cbor:",toarray"`
	Target string   `cbor:"target"`
}

type Character struct {
	_     struct{} `cbor:",toarray"`
	Major uint64   `cbor:"major"`
	Minor uint64   `cbor:"minor"`
}

type Block struct {
	_     struct{} `cbor:",toarray"`
	Major uint64   `cbor:"major"`
	Minor uint64   `cbor:"minor"`
}

type Fifo struct {
	_ struct{} `cbor:",toarray"`
}

func main() {
	rootCmd := cobra.Command{
		Use:  "<oci-image> <output>",
		Args: cobra.ExactArgs(2),
		RunE: runCommand,
	}
	err := rootCmd.Execute()
	if err != nil {
		_, _ = fmt.Fprintf(os.Stderr, "Error: %s\n", err)
		os.Exit(1)
	}
}

func runCommand(cmd *cobra.Command, args []string) error {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	image := args[0]
	output := args[1]

	ociref, err := ocilayout.NewReference(image, "")
	if err != nil {
		panic(err)
	}

	src, err := ociref.NewImageSource(ctx, &types.SystemContext{})
	if err != nil {
		panic(err)
	}

	img, err := ociref.NewImage(ctx, nil)
	if err != nil {
		panic(err)
	}

	var rootid uint = 0
	filesystem := make(map[string]File)
	filesystem["/"] = File{
		Mode:      Mode{},
		Uid:       &rootid,
		Gid:       &rootid,
		Type:      "directory",
		Directory: &Directory{},
	}

	layers := img.LayerInfos()
	for _, layer := range layers {
		blob, _, err := src.GetBlob(ctx, layer, nil)
		if err != nil {
			panic(err)
		}
		switch layer.MediaType {
		case "application/vnd.oci.image.layer.v1.tar+gzip":
			addGZIPLayer(blob, filesystem)
		case "application/vnd.oci.image.layer.v1.tar":
			addLayer(blob, filesystem)
		}
	}

	ts := cbor.NewTagSet()
	ts.Add(cbor.TagOptions{
		DecTag: cbor.DecTagRequired,
		EncTag: cbor.EncTagRequired,
	}, reflect.TypeOf(OCIX{}), 6, 1868786040)

	opts := cbor.CanonicalEncOptions()
	opts.Time = cbor.TimeRFC3339Nano
	encmode, err := opts.EncModeWithTags(ts)
	if err != nil {
		panic(err)
	}
	log.Printf("Writing out filesystem with %d files", len(filesystem))

	f, err := os.OpenFile(output, os.O_RDWR|os.O_CREATE, 0644)
	if err != nil {
		return fmt.Errorf("Could not open output file %q: %w", output, err)
	}
	defer f.Close()
	enc := encmode.NewEncoder(f)
	return enc.Encode(OCIX{
		Version: 0,
		Files:   filesystem,
	})

}

func addLayer(blob io.ReadCloser, fs map[string]File) {
	defer blob.Close()

	reader := tar.NewReader(blob)
	for {
		header, err := reader.Next()
		if err == io.EOF {
			return
		}
		if err != nil {
			panic(err)
		}
		if strings.Contains(header.FileInfo().Name(), "/.wh") {
			log.Fatalf("Found whiteout: %+v\n", header)
		}
		mode := header.FileInfo().Mode() & os.ModePerm

		f := File{
			Mode: Mode{
				User: RWX{
					Read:    mode&syscall.S_IRUSR > 0,
					Write:   mode&syscall.S_IWUSR > 0,
					Execute: mode&syscall.S_IXUSR > 0,
				},
				Group: RWX{
					Read:    mode&syscall.S_IRGRP > 0,
					Write:   mode&syscall.S_IWGRP > 0,
					Execute: mode&syscall.S_IXUSR > 0,
				},
				Other: RWX{
					Read:    mode&syscall.S_IROTH > 0,
					Write:   mode&syscall.S_IWOTH > 0,
					Execute: mode&syscall.S_IXOTH > 0,
				},
				Setuid: mode&syscall.S_ISUID > 0,
				Setgid: mode&syscall.S_ISGID > 0,
				Sticky: mode&syscall.S_ISVTX > 0,
			},
		}

		if header.Uid != 0 {
			uid := uint(header.Uid)
			f.Uid = &uid
		}

		if header.Gid != 0 {
			gid := uint(header.Gid)
			f.Gid = &gid
		}

		if header.Uname != "" {
			username := header.Uname
			f.Username = &username
		}

		if header.Gname != "" {
			groupname := header.Gname
			f.Groupname = &groupname
		}

		if !header.ModTime.IsZero() {
			modtime := header.ModTime
			f.Mtime = &modtime
		}

		if !header.AccessTime.IsZero() {
			atime := header.AccessTime
			f.Atime = &atime
		}

		if !header.ChangeTime.IsZero() {
			ctime := header.ChangeTime
			f.Mtime = &ctime
		}

		if len(header.PAXRecords) > 0 {
			xattrs := make(map[string]interface{})
			for key, value := range header.PAXRecords {
				xattrs[key] = value
			}
			f.Xattr = &xattrs

		}

		switch header.Typeflag {
		case tar.TypeReg:
			hasher := blake3.New()
			teereader := io.TeeReader(reader, hasher)
			data, err := ioutil.ReadAll(teereader)
			if err != nil {
				panic(err)
			}
			if int64(len(data)) != header.Size {
				panic("Size unequal")
			}

			file := Regularfile{
				Size: uint64(header.Size),
			}
			buf := make([]byte, 0)
			file.Blake3Hash = hasher.Sum(buf)
			f.Regularfile = &file
			f.Type = "regularfile"
		case tar.TypeLink:
			f.Link = &Link{
				Target: header.Linkname,
			}
			f.Type = "link"
		case tar.TypeSymlink:
			f.SymLink = &SymLink{
				Target: header.Linkname,
			}
			f.Type = "symlink"
		case tar.TypeChar:
			f.Character = &Character{
				Major: uint64(header.Devmajor),
				Minor: uint64(header.Devminor),
			}
			f.Type = "character"
		case tar.TypeBlock:
			f.Block = &Block{
				Major: uint64(header.Devmajor),
				Minor: uint64(header.Devminor),
			}
			f.Type = "block"
		case tar.TypeDir:
			f.Directory = &Directory{}
			f.Type = "directory"
		case tar.TypeFifo:
			f.Fifo = &Fifo{}
			f.Type = "fifo"
		}
		log.Printf("Adding file: %s", header.Name)
		fs[filepath.Join("/", header.Name)] = f
	}
}

func addGZIPLayer(blob io.ReadCloser, fs map[string]File) {
	defer blob.Close()
	reader, err := gzip.NewReader(blob)
	if err != nil {
		panic(err)
	}
	addLayer(ioutil.NopCloser(reader), fs)
}
