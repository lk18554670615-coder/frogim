import type { AdminUserBatchInput, AdminUserBatchItemResult, UserRecord } from './types';

export const MAX_USER_IMPORT_ROWS = 100;
export const MAX_USER_IMPORT_BYTES = 2 * 1024 * 1024;

export interface UserImportPreviewRow extends AdminUserBatchInput {
  rawGender: string;
  valid: boolean;
  validationCode?: string;
  validationMessage?: string;
}

export interface RawUserImportRow {
  clientRow: number;
  phone: string;
  name: string;
  password: string;
  gender: string;
  formulaField?: string;
}

interface ParsedCell {
  text: string;
  formula: boolean;
}

interface ParsedMatrixRow {
  clientRow: number;
  cells: ParsedCell[];
}

type ImportField = 'phone' | 'name' | 'gender' | 'password';

const headerAliases: Record<string, ImportField> = {
  '手机号': 'phone', phone: 'phone',
  '昵称': 'name', name: 'name',
  '性别': 'gender', gender: 'gender',
  '初始密码': 'password', password: 'password',
};

const genderValues: Record<string, UserRecord['gender']> = {
  '': 'unspecified', '未设置': 'unspecified', unspecified: 'unspecified',
  '男': 'male', male: 'male',
  '女': 'female', female: 'female',
};

function normalizeHeader(value: string) {
  return value.replace(/^\uFEFF/, '').trim().toLowerCase();
}

function normalizePhone(value: string) {
  return value.trim().replace(/^\+86/, '');
}

function rowError(row: UserImportPreviewRow, code: string, message: string) {
  row.valid = false;
  row.validationCode = code;
  row.validationMessage = message;
}

export function validateUserImportRows(rawRows: RawUserImportRow[], passwordMinLength = 8, passwordMaxBytes = 72): UserImportPreviewRow[] {
  if (rawRows.length > MAX_USER_IMPORT_ROWS) throw new Error(`单次最多导入 ${MAX_USER_IMPORT_ROWS} 个用户`);
  const phoneCounts = new Map<string, number>();
  rawRows.forEach((raw) => {
    const phone = normalizePhone(raw.phone);
    if (/^1[3-9]\d{9}$/.test(phone)) phoneCounts.set(phone, (phoneCounts.get(phone) ?? 0) + 1);
  });
  return rawRows.map((raw) => {
    const phone = normalizePhone(raw.phone);
    const normalizedGender = genderValues[raw.gender.trim().toLowerCase()];
    const row: UserImportPreviewRow = {
      clientRow: raw.clientRow, phone, name: raw.name.trim(), password: raw.password,
      gender: normalizedGender ?? 'unspecified', rawGender: raw.gender.trim(), valid: true,
    };
    if (raw.formulaField) {
      rowError(row, 'FORMULA_NOT_ALLOWED', `${raw.formulaField}不能使用公式`);
    } else if (!/^1[3-9]\d{9}$/.test(phone)) {
      rowError(row, 'INVALID_PHONE', '请输入有效的 11 位中国大陆手机号');
    } else if (!row.name || [...row.name].length > 40) {
      rowError(row, 'INVALID_NAME', '昵称不能为空且不能超过 40 个字符');
    } else if ([...raw.password].length < passwordMinLength || new TextEncoder().encode(raw.password).length > passwordMaxBytes) {
      rowError(row, 'INVALID_PASSWORD', `密码至少 ${passwordMinLength} 位且不能超过 ${passwordMaxBytes} 字节`);
    } else if (!normalizedGender) {
      rowError(row, 'INVALID_GENDER', '性别仅支持男、女、未设置或对应英文值');
    } else if ((phoneCounts.get(phone) ?? 0) > 1) {
      rowError(row, 'DUPLICATE_IN_FILE', '手机号在导入文件中重复');
    }
    return row;
  });
}

export async function markExistingUserPhones(
  rows: UserImportPreviewRow[],
  lookup: (phone: string) => Promise<UserRecord[]>,
): Promise<UserImportPreviewRow[]> {
  const phones = [...new Set(rows.filter((row) => row.valid).map((row) => row.phone))];
  const existing = new Set<string>();
  let cursor = 0;
  const worker = async () => {
    while (cursor < phones.length) {
      const phone = phones[cursor++];
      const users = await lookup(phone);
      if (users.some((user) => user.phone === phone)) existing.add(phone);
    }
  };
  await Promise.all(Array.from({ length: Math.min(4, phones.length) }, worker));
  return rows.map((row) => existing.has(row.phone) && row.valid
    ? { ...row, valid: false, validationCode: 'PHONE_ALREADY_EXISTS', validationMessage: '手机号已存在' }
    : row);
}

function matrixToRawRows(rows: ParsedMatrixRow[]): RawUserImportRow[] {
  const headerIndex = rows.findIndex((row) => row.cells.some((cell) => cell.text.trim() || cell.formula));
  if (headerIndex < 0) throw new Error('导入文件为空');
  const headers = new Map<ImportField, number>();
  rows[headerIndex].cells.forEach((cell, index) => {
    const field = headerAliases[normalizeHeader(cell.text)];
    if (!field) return;
    if (headers.has(field)) throw new Error(`表头“${cell.text.trim()}”重复`);
    headers.set(field, index);
  });
  const missing = (['phone', 'name', 'password'] as ImportField[]).filter((field) => !headers.has(field));
  if (missing.length) throw new Error('模板必须包含手机号、昵称和初始密码列');

  const fieldLabel: Record<ImportField, string> = { phone: '手机号', name: '昵称', gender: '性别', password: '初始密码' };
  const values = rows.slice(headerIndex + 1).filter((row) => row.cells.some((cell) => cell.text.trim() || cell.formula)).map((row) => {
    const read = (field: ImportField) => {
      const index = headers.get(field);
      return index === undefined ? { text: '', formula: false } : (row.cells[index] ?? { text: '', formula: false });
    };
    const formulaField = (['phone', 'name', 'gender', 'password'] as ImportField[]).find((field) => read(field).formula);
    return {
      clientRow: row.clientRow,
      phone: read('phone').text,
      name: read('name').text,
      gender: read('gender').text,
      password: read('password').text,
      formulaField: formulaField ? fieldLabel[formulaField] : undefined,
    } satisfies RawUserImportRow;
  });
  if (!values.length) throw new Error('模板中没有用户数据');
  if (values.length > MAX_USER_IMPORT_ROWS) throw new Error(`单次最多导入 ${MAX_USER_IMPORT_ROWS} 个用户，当前文件包含 ${values.length} 行`);
  return values;
}

async function parseCsv(file: File): Promise<ParsedMatrixRow[]> {
  const bytes = await file.arrayBuffer();
  let content: string;
  try {
    content = new TextDecoder('utf-8', { fatal: true }).decode(bytes);
  } catch {
    throw new Error('CSV 必须使用 UTF-8 编码');
  }
  const { default: Papa } = await import('papaparse');
  const parsed = Papa.parse<string[]>(content, { skipEmptyLines: false });
  if (parsed.errors.length) throw new Error(`CSV 第 ${Number(parsed.errors[0].row ?? 0) + 1} 行格式错误：${parsed.errors[0].message}`);
  return parsed.data.map((values, index) => ({
    clientRow: index + 1,
    cells: values.map((value) => ({ text: String(value ?? ''), formula: false })),
  }));
}

async function parseXlsx(file: File): Promise<ParsedMatrixRow[]> {
  const ExcelJS = await import('exceljs');
  const workbook = new ExcelJS.Workbook();
  await workbook.xlsx.load(await file.arrayBuffer() as never);
  const worksheet = workbook.worksheets[0];
  if (!worksheet) throw new Error('Excel 文件中没有工作表');
  const rows: ParsedMatrixRow[] = [];
  worksheet.eachRow({ includeEmpty: true }, (excelRow, rowNumber) => {
    const cells: ParsedCell[] = [];
    for (let column = 1; column <= Math.max(excelRow.cellCount, worksheet.columnCount); column++) {
      const cell = excelRow.getCell(column);
      const value = cell.value;
      cells.push({
        text: cell.text ?? '',
        formula: Boolean(value && typeof value === 'object' && 'formula' in value),
      });
    }
    rows.push({ clientRow: rowNumber, cells });
  });
  return rows;
}

export async function parseUserImportFile(file: File, passwordMinLength = 8, passwordMaxBytes = 72) {
  if (file.size > MAX_USER_IMPORT_BYTES) throw new Error('导入文件不能超过 2 MB');
  const extension = file.name.toLowerCase().split('.').pop();
  if (extension !== 'csv' && extension !== 'xlsx') throw new Error('仅支持 .xlsx 和 .csv 文件');
  const matrix = extension === 'csv' ? await parseCsv(file) : await parseXlsx(file);
  return validateUserImportRows(matrixToRawRows(matrix), passwordMinLength, passwordMaxBytes);
}

function downloadBlob(blob: Blob, filename: string) {
  const url = URL.createObjectURL(blob);
  const anchor = document.createElement('a');
  anchor.href = url;
  anchor.download = filename;
  document.body.appendChild(anchor);
  anchor.click();
  anchor.remove();
  URL.revokeObjectURL(url);
}

export async function downloadUserImportTemplate(format: 'xlsx' | 'csv') {
  const headers = ['手机号', '昵称', '性别', '初始密码'];
  const example = ['13800138000', '示例用户', '未设置', 'StrongPass123!'];
  if (format === 'csv') {
    const { default: Papa } = await import('papaparse');
    const csv = `\uFEFF${Papa.unparse([headers, example])}`;
    downloadBlob(new Blob([csv], { type: 'text/csv;charset=utf-8' }), '批量新增用户模板.csv');
    return;
  }
  const ExcelJS = await import('exceljs');
  const workbook = new ExcelJS.Workbook();
  const worksheet = workbook.addWorksheet('用户导入');
  worksheet.addRow(headers);
  worksheet.addRow(example);
  worksheet.columns = [{ width: 18 }, { width: 24 }, { width: 14 }, { width: 24 }];
  worksheet.getColumn(4).numFmt = '@';
  worksheet.getRow(1).font = { bold: true };
  const buffer = await workbook.xlsx.writeBuffer();
  downloadBlob(new Blob([buffer as BlobPart], { type: 'application/vnd.openxmlformats-officedocument.spreadsheetml.sheet' }), '批量新增用户模板.xlsx');
}

function spreadsheetSafe(value: string) {
  return /^[=+\-@]/.test(value) ? `'${value}` : value;
}

export function buildUserImportResultRows(rows: UserImportPreviewRow[], results: AdminUserBatchItemResult[]) {
  const byRow = new Map(results.map((result) => [result.clientRow, result]));
  return rows.map((row) => {
    const result = byRow.get(row.clientRow);
    const created = result?.status === 'created';
    return {
      '原始行号': row.clientRow,
      '手机号': row.phone,
      '昵称': spreadsheetSafe(row.name),
      '性别': row.rawGender || '未设置',
      '结果': created ? '创建成功' : '创建失败',
      '用户ID': result?.user?.id ?? '',
      '错误代码': result?.code ?? row.validationCode ?? '',
      '失败原因': result?.message ?? row.validationMessage ?? '',
    };
  });
}

export async function downloadUserImportResult(rows: UserImportPreviewRow[], results: AdminUserBatchItemResult[]) {
  const data = buildUserImportResultRows(rows, results);
  const { default: Papa } = await import('papaparse');
  downloadBlob(new Blob([`\uFEFF${Papa.unparse(data)}`], { type: 'text/csv;charset=utf-8' }), `批量新增用户结果-${new Date().toISOString().slice(0, 10)}.csv`);
}
