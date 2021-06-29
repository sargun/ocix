# OCIX
Sargun's little place for OCI image related experiments.

None of these experiments nececessarily represent long-term views.

## OCIX Format
The OCIX format described in `ocix.cddl` describes a way to describe a filesystem in a canonical manner.
It is a CDDL file, which is a way to describe CBOR encoded data.
The data is meant to be encoded according to the Canonical encoding options.

The ideas of this are somewhat similar to (mtree)[https://linux.die.net/man/8/mtree].
It decouples the filesystem layout information and the files.
It also tries to come up with the minimal descriptor for a filesystem.

Once you have the manifest, you can download the content-addressable blobs referenced by it.

There is a tool in `cmd`, called `cbormanifest` to create one of these OCIX manifests.

### Usage of cbormanifest
cbormanifest allows you to create a manifest of an OCI image.

Example, on ubuntu image:

```
# Install gobin: https://github.com/myitcv/gobin
gobin -run github.com/containers/skopeo/cmd/skopeo@v1.3.0 copy --insecure-policy --override-os linux  docker://docker.io/python@sha256:f42d92068b29045b6893da82032ca4fcf96193be5dcbdcfcba948489efa9e832 oci:python:test

go run ./cmd/cbormanifest python:test python.ocix
# This will generate python.ocix
```

We can now inspect `python.ocix`:

```
$ sha256sum python.ocix 
814620d790fe425c45066b4268f9492992c900197a76abfe98cace7e9e26d712  python.ocix
$ wc -c python.ocix 
 1120268 python.ocix
$ zstd -9 python.ocix 
python.ocix          : 21.39%   (1120268 => 239589 bytes, python.ocix.zst)    
$ wc -c python.ocix.zst 
  239589 python.ocix.zst

# Let's unbundle this image:
gobin -run github.com/opencontainers/umoci/cmd/umoci@v0.4.7 unpack --image python:test --rootless bundle

$ find bundle/rootfs/ -type f -exec cat {} \;|wc -c
 113395561
$ tar cf rootfs.tar bundle/rootfs/
$ wc -c rootfs.tar 
 115204096 rootfs.tar

# 1808535 bytes of overhead is 1.8MB, putting it in a similar area to 1.1 MB cost of OCIX
```

## Known Problems
### "Ownership"
The ownership section is pretty ambigious:

```
owner = {
    $$owner,
}

$$owner //= ()


$$owner //= (
    uid: unsigned,
    gid: unsigned,
)

$$owner //= (
    username: tstr,
    ? groupname: tstr
)
```

### Holes
There's no way to describe holes ([sparse files](https://en.wikipedia.org/wiki/Sparse_file)) in the format.
A "smart" client can take a blob and put holes in it as it writes it, but that requires the client to do a lot of heavy lifting.

## What now?
Downloading ~1000s of little files to launch an image seems like it'd be pretty terrrible.
It might not be.

Why?

1. You don't actually need most files. At least not all at the same time. Fetching can be prioritized based on access.
2. HTTP/2 (currently) supports push. There's no reason the registry can't help.
3. There's no reason why these need to be discrete files -- in fact, there might be a place for saying "serve this up as a custom tarball" or some such. 
4. Custom shared (zstd) compression dictionaries can be highly effective.
5. Blake3 allows for Bao, which might open clever block-level fetching.

A POC probably needs to get built with some of the above ideas.