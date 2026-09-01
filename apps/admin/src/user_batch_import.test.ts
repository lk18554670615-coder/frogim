import ExcelJS from 'exceljs';
import { describe, expect, it } from 'vitest';
import { buildUserImportResultRows, markExistingUserPhones, parseUserImportFile, validateUserImportRows, type RawUserImportRow } from './user_batch_import';

function importFile(name: string, content: string | ArrayBuffer | Uint8Array): File {
  const bytes = typeof content === 'string' ? new TextEncoder().encode(content) : content instanceof Uint8Array ? content : new Uint8Array(content);
  return { name, size: bytes.byteLength, arrayBuffer: async () => bytes.buffer.slice(bytes.byteOffset, bytes.byteOffset + bytes.byteLength) } as File;
}

describe('批量用户文件解析', () => {
  it('解析 UTF-8 CSV、中英文性别并标记文件内重复', async () => {
    const csv = '\uFEFF手机号,昵称,性别,初始密码\n+8613800138001,用户一,女,StrongPass123!\n13800138001,重复用户,female,StrongPass123!\n13900139002,用户二,未设置,AnotherPass123!\n';
    const rows = await parseUserImportFile(importFile('users.csv', csv));
    expect(rows).toHaveLength(3);
    expect(rows[0]).toEqual(expect.objectContaining({ clientRow: 2, phone: '13800138001', gender: 'female', valid: false, validationCode: 'DUPLICATE_IN_FILE' }));
    expect(rows[1]).toEqual(expect.objectContaining({ clientRow: 3, valid: false, validationCode: 'DUPLICATE_IN_FILE' }));
    expect(rows[2]).toEqual(expect.objectContaining({ clientRow: 4, gender: 'unspecified', valid: true }));
  });

  it('预检数据库手机号并仅按完全匹配标记已存在账号', async () => {
    const rows = validateUserImportRows([
      { clientRow: 2, phone: '13800138001', name: '已有用户', password: 'StrongPass123!', gender: 'female' },
      { clientRow: 3, phone: '13900139002', name: '新用户', password: 'StrongPass123!', gender: 'male' },
    ]);
    const checked = await markExistingUserPhones(rows, async (phone) => phone === '13800138001'
      ? [{ id: 'u_existing', phone, nickname: '已有用户' } as never]
      : [{ id: 'u_fuzzy', phone: '13900139009', nickname: '模糊匹配' } as never]);
    expect(checked[0]).toEqual(expect.objectContaining({ valid: false, validationCode: 'PHONE_ALREADY_EXISTS' }));
    expect(checked[1]).toEqual(expect.objectContaining({ valid: true }));
  });

  it('读取 XLSX 第一个工作表并拒绝公式单元格', async () => {
    const workbook = new ExcelJS.Workbook();
    const worksheet = workbook.addWorksheet('用户导入');
    worksheet.addRow(['手机号', '昵称', '性别', '初始密码']);
    worksheet.addRow(['13800138001', '公式用户', '男', 'StrongPass123!']);
    worksheet.getCell('A2').value = { formula: '10000000000+3800138001', result: 13800138001 };
    const buffer = await workbook.xlsx.writeBuffer();
    const bytes = buffer instanceof ArrayBuffer ? buffer : new Uint8Array(buffer as ArrayLike<number>);
    const rows = await parseUserImportFile(importFile('users.xlsx', bytes));
    expect(rows).toEqual([expect.objectContaining({ clientRow: 2, valid: false, validationCode: 'FORMULA_NOT_ALLOWED', validationMessage: '手机号不能使用公式' })]);
  });

  it('校验字段、动态密码边界和 100 行上限', () => {
    const invalid = validateUserImportRows([
      { clientRow: 2, phone: '12800138000', name: '错误号码', password: 'StrongPass123!', gender: 'male' },
      { clientRow: 3, phone: '13800138001', name: '', password: 'StrongPass123!', gender: 'female' },
      { clientRow: 4, phone: '13800138002', name: '短密码', password: '1234567', gender: 'female' },
      { clientRow: 5, phone: '13800138003', name: '错误性别', password: 'StrongPass123!', gender: 'unknown' },
    ]);
    expect(invalid.map((row) => row.validationCode)).toEqual(['INVALID_PHONE', 'INVALID_NAME', 'INVALID_PASSWORD', 'INVALID_GENDER']);
    const maximum: RawUserImportRow[] = Array.from({ length: 100 }, (_, index) => ({ clientRow: index + 2, phone: `138${String(index).padStart(8, '0')}`, name: '用户', password: 'StrongPass123!', gender: '' }));
    expect(validateUserImportRows(maximum)).toHaveLength(100);
    const tooMany: RawUserImportRow[] = Array.from({ length: 101 }, (_, index) => ({ clientRow: index + 2, phone: `138${String(index).padStart(8, '0')}`, name: '用户', password: 'StrongPass123!', gender: '' }));
    expect(() => validateUserImportRows(tooMany)).toThrow('单次最多导入 100 个用户');
  });

  it('支持英文表头并在读取前拒绝超过 2 MB 的文件', async () => {
    const rows = await parseUserImportFile(importFile('users.csv', 'phone,name,gender,password\n13800138001,English Header,male,StrongPass123!'));
    expect(rows).toEqual([expect.objectContaining({ valid: true, gender: 'male' })]);
    const oversized = { name: 'too-large.csv', size: 2 * 1024 * 1024 + 1, arrayBuffer: async () => new ArrayBuffer(0) } as File;
    await expect(parseUserImportFile(oversized)).rejects.toThrow('导入文件不能超过 2 MB');
  });

  it('结果报告不包含密码并对电子表格公式前缀转义', () => {
    const [row] = validateUserImportRows([{ clientRow: 2, phone: '13800138001', name: '=危险昵称', password: 'StrongPass123!', gender: 'female' }]);
    const report = buildUserImportResultRows([row], [{ clientRow: 2, status: 'created', user: { id: 'u_1' } as never }]);
    expect(report[0]).toEqual(expect.objectContaining({ '昵称': "'=危险昵称", '结果': '创建成功', '用户ID': 'u_1' }));
    expect(Object.keys(report[0]).some((key) => key.includes('密码'))).toBe(false);
    expect(JSON.stringify(report)).not.toContain('StrongPass123!');
  });
});
