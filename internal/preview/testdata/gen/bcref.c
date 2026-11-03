#define BCDEC_IMPLEMENTATION
#define BCDEC_BC4BC5_PRECISE
#include "bcdec.h"
#include <stdio.h>
#include <stdlib.h>
#include <string.h>
int main(int argc, char **argv) {
  const char *f = argv[1];
  FILE *in = fopen(argv[2], "rb"), *out = fopen(argv[3], "wb");
  int bs = (!strcmp(f,"bc1")||!strcmp(f,"bc4")||!strcmp(f,"bc4s")) ? 8 : 16;
  unsigned char blk[16];
  while (fread(blk, 1, bs, in) == (size_t)bs) {
    if (!strcmp(f,"bc1")) { unsigned char o[64]; bcdec_bc1(blk,o,16); fwrite(o,1,64,out); }
    else if (!strcmp(f,"bc2")) { unsigned char o[64]; bcdec_bc2(blk,o,16); fwrite(o,1,64,out); }
    else if (!strcmp(f,"bc3")) { unsigned char o[64]; bcdec_bc3(blk,o,16); fwrite(o,1,64,out); }
    else if (!strcmp(f,"bc7")) { unsigned char o[64]; bcdec_bc7(blk,o,16); fwrite(o,1,64,out); }
    else if (!strcmp(f,"bc4")) { float o[16]; bcdec_bc4_float(blk,o,4,0); fwrite(o,4,16,out); }
    else if (!strcmp(f,"bc4s")) { float o[16]; bcdec_bc4_float(blk,o,4,1); fwrite(o,4,16,out); }
    else if (!strcmp(f,"bc5")) { float o[32]; bcdec_bc5_float(blk,o,8,0); fwrite(o,4,32,out); }
    else if (!strcmp(f,"bc6h")) { float o[48]; bcdec_bc6h_float(blk,o,12,0); fwrite(o,4,48,out); }
    else if (!strcmp(f,"bc6hs")) { float o[48]; bcdec_bc6h_float(blk,o,12,1); fwrite(o,4,48,out); }
  }
  fclose(in); fclose(out); return 0;
}
