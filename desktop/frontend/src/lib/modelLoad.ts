import { AnimationClip, Box3, LoadingManager, Object3D, type BufferGeometry, type Material, type Mesh, type Texture } from "three";
import { FBXLoader } from "three/examples/jsm/loaders/FBXLoader.js";
import { GLTFLoader } from "three/examples/jsm/loaders/GLTFLoader.js";
import { MTLLoader } from "three/examples/jsm/loaders/MTLLoader.js";
import { OBJLoader } from "three/examples/jsm/loaders/OBJLoader.js";
import { USDLoader } from "three/examples/jsm/loaders/USDLoader.js";
import type { ModelSide, ModelStats } from "@/api/desktop";
import { base64ToBytes } from "@/lib/utils";

export type LoadedModel = { object: Object3D; animations: AnimationClip[] };

export const clipName = (clip: AnimationClip, i: number): string => clip.name || `Animation ${i + 1}`;

export const renderableFormats = ["glb", "gltf", "obj", "fbx", "usd", "usda", "usdc", "usdz"];

export { base64ToBytes };

const mimeTypes: Record<string, string> = {
  png: "image/png",
  jpg: "image/jpeg",
  jpeg: "image/jpeg",
  webp: "image/webp",
  gif: "image/gif",
  ktx2: "image/ktx2",
  bin: "application/octet-stream",
};

export function mimeFor(path: string): string {
  return mimeTypes[path.split(".").pop()?.toLowerCase() ?? ""] ?? "application/octet-stream";
}

export function cleanPath(p: string): string {
  const out: string[] = [];
  for (const part of p.split("/")) {
    if (part === "" || part === ".") continue;
    if (part === ".." && out.length && out[out.length - 1] !== "..") out.pop();
    else out.push(part);
  }
  return out.join("/");
}

const blankPNG = "data:image/png;base64,iVBORw0KGgoAAAANSUhEUgAAAAEAAAABCAQAAAC1HAwCAAAAC0lEQVR42mNkYAAAAAYAAjCB0C8AAAAASUVORK5CYII=";

const decode = (url: string) => {
  try {
    return decodeURIComponent(url);
  } catch {
    return url;
  }
};

// fileKey matches a loader URL to a key of the model's files the way the
// daemon writes them: path-cleaned and URL-decoded.
export function fileKey(files: Record<string, string>): (url: string) => string | undefined {
  const keys = new Map<string, string>();
  for (const key of Object.keys(files)) if (!keys.has(cleanPath(key))) keys.set(cleanPath(key), key);
  return (url) => keys.get(cleanPath(url)) ?? keys.get(cleanPath(decode(url)));
}

// fileManager serves the model's referenced files from memory and never lets
// a loader reach the network: unknown URLs get an empty placeholder.
export function fileManager(files: Record<string, string>): LoadingManager {
  const lookup = fileKey(files);
  const manager = new LoadingManager();
  manager.setURLModifier((url) => {
    if (/^(data|blob):/i.test(url)) return url;
    const key = lookup(url);
    if (key) return `data:${mimeFor(key)};base64,${files[key]}`;
    return mimeFor(url).startsWith("image/") && !url.toLowerCase().endsWith(".ktx2") ? blankPNG : "data:application/octet-stream;base64,";
  });
  return manager;
}

const text = (bytes: Uint8Array) => new TextDecoder().decode(bytes);

const arrayBuffer = (bytes: Uint8Array): ArrayBuffer => bytes.buffer.slice(bytes.byteOffset, bytes.byteOffset + bytes.byteLength) as ArrayBuffer;

// loadModel parses one side's bytes into a three.js object graph.
export async function loadModel(side: ModelSide): Promise<LoadedModel> {
  const files = (side.files ?? {}) as Record<string, string>;
  const manager = fileManager(files);
  const bytes = base64ToBytes(side.data);
  switch (side.format) {
    case "glb":
    case "gltf": {
      const gltf = await new GLTFLoader(manager).parseAsync(side.format === "glb" ? arrayBuffer(bytes) : text(bytes), "");
      return { object: gltf.scene, animations: gltf.animations };
    }
    case "obj": {
      const source = text(bytes);
      const loader = new OBJLoader(manager);
      const libs = new Set<string>();
      for (const line of source.split("\n")) {
        const [keyword, ...names] = line.trim().split(/\s+/);
        if (keyword === "mtllib") for (const name of names) libs.add(name);
      }
      let merged: MTLLoader.MaterialCreator | undefined;
      const lookup = fileKey(files);
      for (const lib of libs) {
        const key = lookup(lib);
        if (!key) continue;
        const materials = new MTLLoader(manager).parse(text(base64ToBytes(files[key])), key.slice(0, key.lastIndexOf("/") + 1));
        materials.preload();
        if (!merged) merged = materials;
        else for (const [name, m] of Object.entries(materials.materials)) merged.materials[name] ??= m;
      }
      if (merged) loader.setMaterials(merged);
      return { object: loader.parse(source), animations: [] };
    }
    case "fbx": {
      const object = new FBXLoader(manager).parse(arrayBuffer(bytes), "");
      return { object, animations: object.animations };
    }
    case "usda":
      return { object: new USDLoader(manager).parse(text(bytes), ""), animations: [] };
    case "usd":
    case "usdc":
    case "usdz":
      return { object: new USDLoader(manager).parse(arrayBuffer(bytes), ""), animations: [] };
  }
  throw new Error(`${side.format.toUpperCase()} models cannot be shown yet`);
}

const textureValues = (m: Material) => Object.values(m).filter((v) => (v as { isTexture?: boolean } | null)?.isTexture) as Texture[];

export function disposeModel(model: LoadedModel): void {
  model.object.traverse((o) => {
    const mesh = o as Mesh;
    if (!mesh.isMesh) return;
    mesh.geometry.dispose();
    const list: Material[] = Array.isArray(mesh.material) ? mesh.material : [mesh.material];
    for (const m of list) {
      for (const t of textureValues(m)) t.dispose();
      m.dispose();
    }
  });
}

// sceneStats counts what a loaded scene contains, for formats the daemon
// cannot read statistics from.
export function sceneStats(model: LoadedModel): ModelStats {
  let meshes = 0;
  let vertices = 0;
  let triangles = 0;
  let morphTargets = 0;
  const bones = new Set<string>();
  const materials = new Set<Material>();
  const textures = new Set<unknown>();
  let nodes = 0;
  model.object.traverse((o) => {
    nodes++;
    if ((o as { isBone?: boolean }).isBone) bones.add(o.uuid);
    const mesh = o as Mesh;
    if (!mesh.isMesh) return;
    meshes++;
    const g = mesh.geometry as BufferGeometry;
    const count = g.attributes.position?.count ?? 0;
    vertices += count;
    triangles += Math.floor((g.index?.count ?? count) / 3);
    morphTargets += g.morphAttributes.position?.length ?? 0;
    const list: Material[] = Array.isArray(mesh.material) ? mesh.material : [mesh.material];
    for (const m of list) materials.add(m);
  });
  for (const m of materials) {
    for (const t of textureValues(m)) textures.add(t);
  }
  const box = new Box3().setFromObject(model.object);
  const empty = box.isEmpty();
  return {
    generator: "",
    meshes,
    primitives: meshes,
    vertices,
    triangles,
    materials: [...materials].map((m, i) => m.name || `Material ${i + 1}`),
    textures: textures.size,
    nodes,
    skins: 0,
    joints: bones.size,
    animations: model.animations.map((clip, i) => clipName(clip, i)),
    morphTargets,
    min: empty ? null : box.min.toArray(),
    max: empty ? null : box.max.toArray(),
  };
}
