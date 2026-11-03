import type { Pixels } from "@/lib/imageCompare";

// loadPixels decodes an image URL into raw RGBA pixels through a canvas.
export function loadPixels(src: string): Promise<Pixels> {
  return new Promise((resolve, reject) => {
    const img = new Image();
    img.onload = () => {
      const canvas = document.createElement("canvas");
      canvas.width = img.naturalWidth;
      canvas.height = img.naturalHeight;
      const ctx = canvas.getContext("2d", { willReadFrequently: true });
      if (!ctx) {
        reject(new Error("Canvas is not available"));
        return;
      }
      ctx.drawImage(img, 0, 0);
      const { data } = ctx.getImageData(0, 0, canvas.width, canvas.height);
      resolve({ width: canvas.width, height: canvas.height, data });
    };
    img.onerror = () => reject(new Error("The preview could not be decoded"));
    img.src = src;
  });
}

export function drawPixels(canvas: HTMLCanvasElement, pixels: Pixels): void {
  canvas.width = pixels.width;
  canvas.height = pixels.height;
  const ctx = canvas.getContext("2d");
  if (!ctx) return;
  const image = ctx.createImageData(pixels.width, pixels.height);
  image.data.set(pixels.data);
  ctx.putImageData(image, 0, 0);
}
