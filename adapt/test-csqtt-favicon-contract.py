#!/usr/bin/env python3
"""Check white-background, blue C favicon consistently in SVG, ICO and mobile PNG."""
from pathlib import Path
import struct,zlib
R=Path(__file__).resolve().parent
SVG=(R/'favicon.svg').read_text(encoding='utf-8')
assert 'viewBox="0 0 64 64"' in SVG
assert '<rect width="64" height="64" fill="#ffffff"/>' in SVG
assert 'stroke="#1E73E8"' in SVG and 'stroke-linecap="round"' in SVG
assert '<path d="M43.5 15.6 A20 20 0 1 0 43.5 48.4"' in SVG
assert 'linearGradient' not in SVG and 'url(#' not in SVG
VERSION='blue-c-white-20261124'
for page in ('csqtt-login.html','csqtt-panel-preview.html'):
 html=(R/page).read_text(encoding='utf-8')
 for filename in ('favicon.svg','favicon.ico','apple-touch-icon.png'):
  assert html.count('href="/'+filename+'?v='+VERSION+'"')==1,(page,filename)
def pixels_png(data):
 assert data[:8]==b'\x89PNG\r\n\x1a\n'
 pos=8;idat=[];info=None
 while pos < len(data):
  n=struct.unpack('>I',data[pos:pos+4])[0]
  typ=data[pos+4:pos+8];payload=data[pos+8:pos+8+n]
  if typ==b'IHDR':
   info=struct.unpack('>IIBBBBB',payload)
  if typ==b'IDAT':idat.append(payload)
  pos+=12+n
 assert info is not None
 width,height,bit_depth,color,compress,filtr,interlace=info
 assert (bit_depth,color,compress,filtr,interlace)==(8,2,0,0,0),info
 raw=zlib.decompress(b''.join(idat))
 bpp=3;stride=width*3;offset=0;prior=bytearray(stride);rgb=[]
 for _ in range(height):
  filt=raw[offset];offset+=1
  src=raw[offset:offset+stride];offset+=stride
  cur=bytearray(stride)
  for i,v in enumerate(src):
   left=cur[i-bpp] if i>=bpp else 0
   above=prior[i]
   upperleft=prior[i-bpp] if i>=bpp else 0
   if filt==0:predictor=0
   elif filt==1:predictor=left
   elif filt==2:predictor=above
   elif filt==3:predictor=(left+above)//2
   elif filt==4:
    p=left+above-upperleft
    da,db,dc=abs(p-left),abs(p-above),abs(p-upperleft)
    predictor=left if da<=db and da<=dc else above if db<=dc else upperleft
   else:raise AssertionError('unexpected PNG filter '+str(filt))
   cur[i]=(v+predictor)&255
  for i in range(0,stride,3):rgb.append(tuple(cur[i:i+3]))
  prior=cur
 assert len(rgb)==width*height
 assert rgb[0]==(255,255,255) and rgb[-1]==(255,255,255)
 blue=sum(1 for x in rgb if x[2]>180 and x[0]<70 and x[1]>60 and x[1]<180)
 white=sum(1 for x in rgb if x==(255,255,255))
 assert blue>width*height*.045,(width,blue)
 assert white>width*height*.52,(width,white)
 return width,height
ico=(R/'favicon.ico').read_bytes()
assert ico[:6]==b'\x00\x00\x01\x00\x04\x00'
sizes=[]
for i in range(4):
 width,height,colors,reserved,planes,depth,length,offset=struct.unpack_from('<BBBBHHII',ico,6+16*i)
 assert colors==0 and reserved==0 and planes==1 and depth==32
 payload=ico[offset:offset+length]
 assert pixels_png(payload)==(width,height)
 sizes.append(width)
assert sizes==[16,32,48,64],sizes
assert pixels_png((R/'apple-touch-icon.png').read_bytes())==(180,180)
print('BLUE_C_WHITE_FAVICON_CONTRACT_PASS: SVG, ICO 16/32/48/64, PNG 180; links cache-busted')
