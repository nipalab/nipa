import { AnimationClip, Box3, LoadingManager, Object3D, type BufferGeometry, type Material, type Mesh, type Texture } from "three";
import { FBXLoader } from "three/examples/jsm/loaders/FBXLoader.js";
import { GLTFLoader } from "three/examples/jsm/loaders/GLTFLoader.js";
import { MTLLoader } from "three/examples/jsm/loaders/MTLLoader.js";
import { OBJLoader } from "three/examples/jsm/loaders/OBJLoader.js";
import { USDLoader } from "three/examples/jsm/loaders/USDLoader.js";
import type { ModelSide, ModelStats } from "@/api/desktop";

export type LoadedModel = { object: Object3D; animations: AnimationClip[] };

export const clipName = (clip: AnimationClip, i: number): string => clip.name || `Animation ${i + 1}`;

export const renderableFormats = ["glb", "gltf", "obj", "fbx", "usd", "usda", "usdc", "usdz"];

export function base64ToBytes(b64: string): Uint8Array {
  const bin = atob(b64);
  const out = new Uint8Array(bin.length);
  for (let i = 0; i < bin.length; i++) out[i] = bin.charCodeAt(i);
  return out;
}

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

// fileManager serves the model's referenced files from memory: loaders ask
// for relative URLs, which are matched as written or URL-decoded.
export function fileManager(files: Record<string, string>): LoadingManager {
  const manager = new LoadingManager();
  manager.setURLModifier((url) => {
    const clean = url.replace(/^\.\//, "");
    let key = clean in files ? clean : undefined;
    if (!key) {
      try {
        const decoded = decodeURIComponent(clean);
        if (decoded in files) key = decoded;
      } catch {
        key = undefined;
      }
    }
    return key ? `data:${mimeFor(key)};base64,${files[key]}` : url;
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
      const lib = /^mtllib\s+(\S+)/m.exec(source)?.[1];
      if (lib && files[lib]) {
        const materials = new MTLLoader(manager).parse(text(base64ToBytes(files[lib])), lib.slice(0, lib.lastIndexOf("/") + 1));
        materials.preload();
        loader.setMaterials(materials);
      }
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
