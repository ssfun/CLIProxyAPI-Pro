export type AuthFileExportEntry = {
  name: string;
  content: string;
};

type ZipFileEntry = {
  path: string;
  data: Uint8Array;
  compressedData: Uint8Array;
  compressionMethod: 0 | 8;
  crc32: number;
};

const ZIP_CONCURRENCY = 4;

const crcTable = Array.from({ length: 256 }, (_, index) => {
  let value = index;
  for (let bit = 0; bit < 8; bit += 1) {
    value = value & 1 ? 0xedb88320 ^ (value >>> 1) : value >>> 1;
  }
  return value >>> 0;
});

const getCrc32 = (data: Uint8Array) => {
  let crc = 0xffffffff;
  data.forEach((byte) => {
    crc = crcTable[(crc ^ byte) & 0xff] ^ (crc >>> 8);
  });
  return (crc ^ 0xffffffff) >>> 0;
};

const getDosTimestamp = (date: Date) => {
  const year = Math.max(1980, date.getFullYear());
  return {
    time: (date.getHours() << 11) | (date.getMinutes() << 5) | Math.floor(date.getSeconds() / 2),
    date: ((year - 1980) << 9) | ((date.getMonth() + 1) << 5) | date.getDate(),
  };
};

const writeUint16 = (view: DataView, offset: number, value: number) => {
  view.setUint16(offset, value, true);
};

const writeUint32 = (view: DataView, offset: number, value: number) => {
  view.setUint32(offset, value >>> 0, true);
};

const concatUint8Arrays = (parts: Uint8Array[]) => {
  const output = new Uint8Array(parts.reduce((total, part) => total + part.length, 0));
  let offset = 0;
  parts.forEach((part) => {
    output.set(part, offset);
    offset += part.length;
  });
  return output;
};

const sanitizeZipPathSegment = (value: string) => {
  const sanitized = value
    .replace(/[\\/:*?"<>|]/g, '_')
    .split('')
    .map((character) => (character.charCodeAt(0) <= 0x1f ? '_' : character))
    .join('');
  return sanitized.replace(/^\.+$/, '_').trim() || 'unknown';
};

const getAuthFileZipPath = (entry: AuthFileExportEntry, usedPaths: Set<string>) => {
  const rawName = sanitizeZipPathSegment(entry.name);
  const baseName = rawName.toLowerCase().endsWith('.json') ? rawName : `${rawName}.json`;
  let path = baseName;
  let index = 2;

  while (usedPaths.has(path)) {
    const dotIndex = baseName.toLowerCase().lastIndexOf('.json');
    const stem = dotIndex >= 0 ? baseName.slice(0, dotIndex) : baseName;
    path = `${stem}-${index}.json`;
    index += 1;
  }

  usedPaths.add(path);
  return path;
};

const toArrayBuffer = (data: Uint8Array) => {
  const arrayBuffer = new ArrayBuffer(data.byteLength);
  new Uint8Array(arrayBuffer).set(data);
  return arrayBuffer;
};

const compressZipData = async (data: Uint8Array): Promise<{ data: Uint8Array; method: 0 | 8 }> => {
  const CompressionStreamCtor = (globalThis as typeof globalThis & {
    CompressionStream?: new (format: 'deflate-raw') => TransformStream<Uint8Array, Uint8Array>;
  }).CompressionStream;

  if (!CompressionStreamCtor) return { data, method: 0 };

  try {
    const stream = new Blob([toArrayBuffer(data)])
      .stream()
      .pipeThrough(new CompressionStreamCtor('deflate-raw'));
    return { data: new Uint8Array(await new Response(stream).arrayBuffer()), method: 8 };
  } catch {
    return { data, method: 0 };
  }
};

export const mapWithConcurrency = async <Input, Output>(
  items: Input[],
  concurrency: number,
  mapper: (item: Input, index: number) => Promise<Output>
) => {
  if (items.length === 0) return [] as Output[];
  const results = new Array<Output>(items.length);
  const workerCount = Math.max(1, Math.min(concurrency, items.length));
  let nextIndex = 0;

  await Promise.all(
    Array.from({ length: workerCount }, async () => {
      while (nextIndex < items.length) {
        const index = nextIndex;
        nextIndex += 1;
        results[index] = await mapper(items[index], index);
      }
    })
  );

  return results;
};

export const buildZipArchive = async (entries: AuthFileExportEntry[]) => {
  const encoder = new TextEncoder();
  const usedPaths = new Set<string>();
  const files: ZipFileEntry[] = await mapWithConcurrency(entries, ZIP_CONCURRENCY, async (entry) => {
    const path = getAuthFileZipPath(entry, usedPaths);
    const data = encoder.encode(entry.content);
    const compressed = await compressZipData(data);
    return {
      path,
      data,
      compressedData: compressed.data,
      compressionMethod: compressed.method,
      crc32: getCrc32(data),
    };
  });
  const timestamp = getDosTimestamp(new Date());
  const parts: Uint8Array[] = [];
  const centralDirectoryParts: Uint8Array[] = [];
  let offset = 0;

  files.forEach((file) => {
    const fileName = encoder.encode(file.path);
    const localHeader = new Uint8Array(30 + fileName.length);
    const localView = new DataView(localHeader.buffer);
    writeUint32(localView, 0, 0x04034b50);
    writeUint16(localView, 4, 20);
    writeUint16(localView, 6, 0x0800);
    writeUint16(localView, 8, file.compressionMethod);
    writeUint16(localView, 10, timestamp.time);
    writeUint16(localView, 12, timestamp.date);
    writeUint32(localView, 14, file.crc32);
    writeUint32(localView, 18, file.compressedData.length);
    writeUint32(localView, 22, file.data.length);
    writeUint16(localView, 26, fileName.length);
    localHeader.set(fileName, 30);
    parts.push(localHeader, file.compressedData);

    const centralDirectoryHeader = new Uint8Array(46 + fileName.length);
    const centralView = new DataView(centralDirectoryHeader.buffer);
    writeUint32(centralView, 0, 0x02014b50);
    writeUint16(centralView, 4, 20);
    writeUint16(centralView, 6, 20);
    writeUint16(centralView, 8, 0x0800);
    writeUint16(centralView, 10, file.compressionMethod);
    writeUint16(centralView, 12, timestamp.time);
    writeUint16(centralView, 14, timestamp.date);
    writeUint32(centralView, 16, file.crc32);
    writeUint32(centralView, 20, file.compressedData.length);
    writeUint32(centralView, 24, file.data.length);
    writeUint16(centralView, 28, fileName.length);
    writeUint32(centralView, 42, offset);
    centralDirectoryHeader.set(fileName, 46);
    centralDirectoryParts.push(centralDirectoryHeader);
    offset += localHeader.length + file.compressedData.length;
  });

  const centralDirectory = concatUint8Arrays(centralDirectoryParts);
  const endOfCentralDirectory = new Uint8Array(22);
  const endView = new DataView(endOfCentralDirectory.buffer);
  writeUint32(endView, 0, 0x06054b50);
  writeUint16(endView, 8, files.length);
  writeUint16(endView, 10, files.length);
  writeUint32(endView, 12, centralDirectory.length);
  writeUint32(endView, 16, offset);

  return new Blob([...parts, centralDirectory, endOfCentralDirectory].map(toArrayBuffer), {
    type: 'application/zip',
  });
};

export const downloadBlobFile = (fileName: string, blob: Blob) => {
  const url = URL.createObjectURL(blob);
  const link = document.createElement('a');
  link.href = url;
  link.download = fileName;
  document.body.appendChild(link);
  link.click();
  link.remove();
  URL.revokeObjectURL(url);
};
