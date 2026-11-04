#!/bin/sh
# Regenerates the encoded audio fixtures with GStreamer (vorbisenc, lamemp3enc, flacenc).
set -e
out=$(dirname "$0")/../audio
gst-launch-1.0 -q audiotestsrc wave=sine freq=440 volume=0.5 num-buffers=6 samplesperbuffer=1024 ! audio/x-raw,rate=22050,channels=1 ! audioconvert ! vorbisenc ! oggmux ! filesink location="$out/tone.ogg"
gst-launch-1.0 -q audiotestsrc wave=sine freq=440 volume=0.5 num-buffers=6 samplesperbuffer=1152 ! audio/x-raw,rate=22050,channels=1 ! audioconvert ! lamemp3enc ! filesink location="$out/tone.mp3"
gst-launch-1.0 -q audiotestsrc wave=sine freq=440 volume=0.5 num-buffers=6 samplesperbuffer=1024 ! audio/x-raw,rate=44100,channels=2,format=S16LE ! flacenc ! filesink location="$out/tone.flac"
