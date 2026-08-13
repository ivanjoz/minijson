/**
 * Sentinel for "this position has no default", i.e. its field's json tag carries `omitempty`.
 * A symbol rather than `undefined` because `undefined` is a legitimate absent-default value and
 * `null` is a legitimate default.
 */
const NO_DEFAULT = Symbol('minijson.noDefault');

type TypeDef = {
  /**
   * Field name per emit position. Dense and indexed by position, so resolving one is an array
   * load rather than a dictionary lookup on a numeric-keyed object. Positions the keys header
   * never named stay `undefined`: their field was zero in every record of this payload.
   */
  slotNames: (string | undefined)[];
  /** Zero-value default per emit position, or NO_DEFAULT when the field is `omitempty`. */
  slotDefaults: any[];
  /** Non-omitempty fields the keys header never named, so they have no emit position at all. */
  tailNames: string[];
  tailDefaults: any[];
};

/**
 * Deep-copies a non-primitive default so records never share one mutable value.
 *
 * Only struct and array defaults are objects — every other zero value is a primitive — so this
 * runs rarely, and the values it copies are small plain JSON by construction.
 */
const cloneDefault = (value: any): any => {
  if (Array.isArray(value)) {
    const copy = new Array(value.length);
    for (let i = 0; i < value.length; i++) copy[i] = cloneDefault(value[i]);
    return copy;
  }
  if (value !== null && typeof value === 'object') {
    const copy: Record<string, any> = {};
    for (const key of Object.keys(value)) copy[key] = cloneDefault(value[key]);
    return copy;
  }
  return value;
};

/**
 * Unmarshals a compact JSON array serialized by the minijson Go library.
 * The input should be an array [keys, content].
 *
 * @param encoded The serialized data [keys, content]
 * @returns The unmarshalled object or array
 */
export const unmarshal = (encoded: any): any => {
  if (!Array.isArray(encoded) || encoded.length !== 2) {
    return encoded;
  }

  const [keysDef, content] = encoded;
  if (!Array.isArray(keysDef)) {
    return encoded;
  }

  // Per-type decoding table, built once per payload rather than once per record.
  const keysMap: Record<number, TypeDef> = {};
  for (const k of keysDef) {
    if (!Array.isArray(k)) continue;
    const typeId = k[0];

    const slotNames: (string | undefined)[] = [];
    // Stop at the last complete pair, matching the Go decoder. A trailing element that is not
    // part of a pair is not a field name and must not be read as one.
    for (let i = 1; i + 1 < k.length; i += 2) {
      const idx = k[i];
      if (typeof idx !== 'number') continue;
      while (slotNames.length <= idx) slotNames.push(undefined);
      slotNames[idx] = k[i + 1];
    }

    /*
     * Optional trailing defaults block: {"field":<zero value>, ...} for every field whose json
     * tag has no `omitempty`.
     *
     * minijson omits all zero values from the wire, including those of fields encoding/json would
     * have written. A typed decoder repopulates them from the destination struct; this one has no
     * types, so without the block those fields read back as `undefined` instead of `0`, `""` or
     * `false`. The backend only sends it when MINIJSON_INCLUDE_EMPTY is on, and its absence simply
     * means no field is reconstructed — which is how every payload behaved before it existed.
     *
     * It is the only element of an entry that is a plain object, and it is last, so neither this
     * parser nor an older one that ignores it can mistake it for a (position, name) pair.
     */
    const lastElement = k.length > 1 ? k[k.length - 1] : undefined;
    const defaults =
      lastElement !== null && typeof lastElement === 'object' && !Array.isArray(lastElement)
        ? lastElement
        : null;

    const slotDefaults: any[] = new Array(slotNames.length);
    const tailNames: string[] = [];
    const tailDefaults: any[] = [];

    if (defaults === null) {
      slotDefaults.fill(NO_DEFAULT);
    } else {
      const named = new Set(slotNames);
      for (let i = 0; i < slotNames.length; i++) {
        const name = slotNames[i];
        // hasOwnProperty, not `in`: a field named "constructor" or "toString" would otherwise
        // resolve against Object.prototype and pick up a function as its default.
        slotDefaults[i] =
          name !== undefined && Object.prototype.hasOwnProperty.call(defaults, name)
            ? defaults[name]
            : NO_DEFAULT;
      }
      // Fields with a default but no emit position were zero in every record of this payload, so
      // the keys header never named them. They are the ones a mask-derived block would have lost.
      for (const name of Object.keys(defaults)) {
        if (named.has(name)) continue;
        tailNames.push(name);
        tailDefaults.push(defaults[name]);
      }
    }

    keysMap[typeId] = { slotNames, slotDefaults, tailNames, tailDefaults };
  }

  let lastTypeId: number | null = null;

  /**
   * Decodes a value recursively based on headers.
   */
  const decode = (val: any): any => {
    if (!Array.isArray(val) || val.length === 0) {
      return val;
    }

    const header = val[0];

    // Header 1: New object type
    // Format: [1, [typeId, ...skipIndices], ...values]
    if (header === 1) {
      if (val.length < 2) return val;
      const refBlock = val[1];
      if (!Array.isArray(refBlock)) return val;
      lastTypeId = refBlock[0];
      // The skip indices start right after the type ID, so the ref block doubles as the skip
      // list with an offset of 1 — no separate array or Set has to be built.
      return populate(lastTypeId, val, 2, refBlock, 1);
    }

    // Header 0: Same type as previous object
    // Format: [0, [skipIndices]?, ...values]
    if (header === 0) {
      if (lastTypeId === null) return val;

      // Position 1 of a header-0 record is the skip block whenever it is an array.
      // The Go encoder upholds that invariant by emitting an
      // empty skip block `[]` when the record has nothing to skip and its first
      // value is itself an encoded array — otherwise `[2]` would mean both "skip
      // field 2" and "empty slice", and every following field would shift by one.
      if (Array.isArray(val[1])) {
        return populate(lastTypeId, val, 2, val[1], 0);
      }
      return populate(lastTypeId, val, 1, null, 0);
    }

    // Header 2: Array of values
    // Format: [2, ...items]
    if (header === 2) {
      const result = [];
      for (let i = 1; i < val.length; i++) {
        result.push(decode(val[i]));
      }
      return result;
    }

    // Header 3: Map of key-value pairs
    // Format: [3, key1, val1, key2, val2, ...]
    if (header === 3) {
      const result: Record<string, any> = {};
      for (let i = 1; i < val.length; i += 2) {
        if (i + 1 < val.length) {
          const key = String(val[i]);
          result[key] = decode(val[i + 1]);
        }
      }
      return result;
    }

    // Fallback for plain arrays (header "Other")
    return val.map(decode);
  };

  /**
   * Populates an object of a given type from a record.
   *
   * Reads the values in place out of `record` starting at `valuesFrom` instead of receiving a
   * pre-sliced array, which removes one array allocation and copy per record.
   *
   * `skips` is the record's own skip list — the ref block for a header-1 record, the skip block
   * for a header-0 one — read from `skipsFrom` onwards, or null when the record has none. The Go
   * encoder writes those indices in ascending order (see appendSkipIndices), so a single cursor
   * walks them alongside the emit positions and no Set has to be built per record.
   *
   * Every position is written exactly once, in the same order for every record of a type. Seeding
   * the object with the defaults and overwriting them afterwards would produce the same objects
   * at the cost of a second write per field, so the branch is folded into the single pass instead.
   *
   * Measured against the pre-refactor version on 2000 records with varied skip sets: decoding is
   * 23-29% faster without the defaults block and 7-18% faster with it. Traversing the resulting
   * objects afterwards is a few percent *slower* when the block is on, because every record now
   * carries every non-omitempty field instead of only the ones that had a value.
   */
  const populate = (
    typeId: number,
    record: any[],
    valuesFrom: number,
    skips: any[] | null,
    skipsFrom: number,
  ) => {
    const typeDef = keysMap[typeId];
    if (!typeDef) return record.slice(valuesFrom);

    const { slotNames, slotDefaults, tailNames, tailDefaults } = typeDef;
    const skipEnd = skips === null ? 0 : skips.length;

    const obj: Record<string, any> = {};
    let valueIdx = valuesFrom;
    let skipIdx = skipsFrom;

    for (let i = 0; i < slotNames.length; i++) {
      const fieldName = slotNames[i];

      let hasValue = true;
      if (skipIdx < skipEnd && skips![skipIdx] === i) {
        skipIdx++;
        hasValue = false;
      } else if (valueIdx >= record.length) {
        // Zero fields at the tail of a record are truncated rather than skipped, so running out
        // of values means every remaining position was zero too.
        hasValue = false;
      }

      if (hasValue) {
        if (fieldName !== undefined) {
          obj[fieldName] = decode(record[valueIdx]);
        }
        valueIdx++;
        continue;
      }

      if (fieldName === undefined) continue;
      const fieldDefault = slotDefaults[i];
      if (fieldDefault !== NO_DEFAULT) {
        obj[fieldName] =
          fieldDefault !== null && typeof fieldDefault === 'object'
            ? cloneDefault(fieldDefault)
            : fieldDefault;
      }
    }

    for (let i = 0; i < tailNames.length; i++) {
      const fieldDefault = tailDefaults[i];
      obj[tailNames[i]] =
        fieldDefault !== null && typeof fieldDefault === 'object'
          ? cloneDefault(fieldDefault)
          : fieldDefault;
    }

    return obj;
  };

  return decode(content);
};
