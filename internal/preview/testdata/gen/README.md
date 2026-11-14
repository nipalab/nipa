Fixture generators for `internal/preview` (not run by tests).

- `bcref.c` + `genbcn.py`: block decoder goldens in `../bcn`, decoded by
  [bcdec](https://github.com/iOrange/bcdec) (`bcdec.h` next to `bcref.c`,
  `gcc -O2 -o bcref bcref.c`), with random blocks plus etcpak BC7 output.
- `fixtures.py`: DDS/KTX2/EXR/HDR/PSD files and their `*.ref.png` renders,
  written with ImageMagick 7, Pillow and etcpak (`python3.14 fixtures.py ..`).
- `audio.sh`: the encoded audio fixtures in `../audio` (Ogg Vorbis, MP3,
  FLAC tones), written with GStreamer (`vorbisenc`, `lamemp3enc`, `flacenc`).
- `media/main.go`: the synthetic containers and sidecar formats in
  `../media` (MP4/MOV, WebM/MKV, Blender files in every header flavour and
  compression, Unreal packages with PNG/JPEG thumbnails, a WOFF of Go
  Regular): `go run ./internal/preview/testdata/gen/media` from the repo
  root.
