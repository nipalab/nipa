import random, subprocess, gzip, os, sys
out=sys.argv[1]
rnd=random.Random(7)
sizes={'bc1':8,'bc2':16,'bc3':16,'bc4':8,'bc4s':8,'bc5':16,'bc6h':16,'bc6hs':16,'bc7':16}
for f,bs in sizes.items():
    data=bytes(rnd.getrandbits(8) for _ in range(256*bs))
    if f=='bc7':
        import etcpak
        img=bytes((x*7+y*3+rnd.getrandbits(5))&255 for y in range(32) for x in range(32*4))
        data+=etcpak.compress_bc7(img,32,32,None)
    open('in.bin','wb').write(data)
    subprocess.check_call(['./bcref',f,'in.bin','ref.bin'])
    open(os.path.join(out,f+'.bin.gz'),'wb').write(gzip.compress(data,mtime=0))
    open(os.path.join(out,f+'.ref.gz'),'wb').write(gzip.compress(open('ref.bin','rb').read(),mtime=0))
