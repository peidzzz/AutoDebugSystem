<script setup lang="ts">
import { ref, onMounted, onUnmounted, nextTick, watch, computed, reactive, provide } from 'vue';
import VChart, { THEME_KEY } from 'vue-echarts';
import { use } from 'echarts/core';
import { CanvasRenderer } from 'echarts/renderers';
import { LineChart } from 'echarts/charts';
import {
  GridComponent,
  TooltipComponent,
  TitleComponent,
  ToolboxComponent,
  DataZoomComponent,
  LegendComponent
} from 'echarts/components';

use([
  CanvasRenderer,
  LineChart,
  GridComponent,
  TooltipComponent,
  TitleComponent,
  ToolboxComponent,
  DataZoomComponent,
  LegendComponent
]);

// Provide theme for VChart
provide(THEME_KEY, 'dark'); // Or dynamics based on app theme

// 引入后端方法 (GetVersion, CheckForUpdates, DownloadAndInstallUpdate, QuitApp)
import { GetSerialPorts, OpenSerial, OpenTcpClient, OpenTcpServer, OpenUdp, Close as CloseConnection, SendData, GetVersion, CheckForUpdates, DownloadAndInstallUpdate, QuitApp, GetPwrDevices, GetPwrDevPath, SavePwrDevices, GetTaskConfig, SaveTaskConfig, GetTaskCfgPath, GetAutoCmds, SaveAutoCmds, GetAutoCmdPath, StartTask, StopTask, EndTask, SkipRound, GetTaskRuntime, ImportAutoCmds } from '../wailsjs/go/main/App';
import { EventsOn } from '../wailsjs/runtime/runtime';
import { shallowRef } from 'vue';

// 设置最大缓存大小，例如 500KB 或 1MB
// 这里的 buffer 是字节数，receivedData 是字符数
const MAX_BUFFER_SIZE = 1024 * 1024;

// --- 1. 核心状态 ---
const portList = ref<string[]>([]);
const selectedPort = ref('');
const isConnected = ref(false);

// 标签页：SERIAL / TASK 并排；NETWORK（TCP 客户端、TCP 服务端、UDP）收进右侧“更多”按钮的弹出菜单
type ConnectionMode = 'SERIAL' | 'NETWORK' | 'TASK';
type NetworkMode = 'TCP_CLIENT' | 'TCP_SERVER' | 'UDP';
const mode = ref<ConnectionMode>('SERIAL');
const netMode = ref<NetworkMode>('TCP_CLIENT');
// “更多”按钮的弹出菜单开关（沿用原项目的 dropdown-fade 弹出效果）
const showMoreMenu = ref(false);

// 切换标签页（已连接串口时也允许切换，连接不会断开）
const switchMode = (m: ConnectionMode) => {
  mode.value = m;
  showMoreMenu.value = false;
};

// 左侧标签页定义：SERIAL 与 TASK 并排
const modeTabs: { key: ConnectionMode; label: string }[] = [
  { key: 'SERIAL', label: 'SERIAL' },
  { key: 'TASK', label: 'TASK' }
];

// NETWORK 标签页下的三个子模式
const netModeTabs: { key: NetworkMode; label: string }[] = [
  { key: 'TCP_CLIENT', label: 'TCP CLIENT' },
  { key: 'TCP_SERVER', label: 'TCP SERVER' },
  { key: 'UDP', label: 'UDP' }
];

// 切换 NETWORK 标签页下的子模式
const switchNetMode = (m: NetworkMode) => {
  if (isConnected.value) {
    isShaking.value = true;
    setTimeout(() => { isShaking.value = false; }, 500);
    return;
  }
  netMode.value = m;
};

// 震动动画状态
const isShaking = ref(false);

// Serial 参数
const baudRate = ref(115200);
const dataBits = ref(8);
const stopBits = ref(1);
const parity = ref('None');
// 典型波特率：下拉只给常用值，避免手输笔误连不上（12 项与设备参数弹窗保持一致）
const baudOptions = [4800, 9600, 14400, 19200, 38400, 57600, 115200, 128000, 230400, 256000, 460800, 921600];

// Network 参数
const netIp = ref('127.0.0.1');
const netPort = ref('43211');
const udpLocalPort = ref('8081');

// --- TASK：设备选择（数据来自可执行文件同目录的 pwr_dev.ini）---
interface PwrDevice {
  name: string;
  port: string;
  baud: number;
  dataBits: number;
  stopBits: number; // 内部值：1 / 15 / 2（15 表示 1.5）
  parity: string;
  powerOn: string;
  powerOff: string;
  powerOnHex: boolean;
  powerOnEnter: boolean;
  powerOffHex: boolean;
  powerOffEnter: boolean;
}

// 新建设备时的缺省值（与 Go 侧 withDefaults() 对齐）
const emptyDeviceDraft = (): PwrDevice => ({
  name: '',
  port: '',
  baud: 9600,
  dataBits: 8,
  stopBits: 1,
  parity: 'None',
  powerOn: '',
  powerOff: '',
  powerOnHex: true,
  powerOnEnter: false,
  powerOffHex: true,
  powerOffEnter: false
});

// 停止位内部值归一化：1 / 1.5 / 15 / 2 → 1 / 15 / 2
const normalizeStopBits = (v: unknown): number => {
  const n = Number(v);
  if (n === 2) return 2;
  if (n === 15 || n === 1.5) return 15;
  return 1;
};

// 设备弹窗里的串口参数下拉：同时只允许打开一个，键为 `行号:参数名`
const deviceMenu = ref('');
const toggleDeviceMenu = (key: string) => {
  deviceMenu.value = deviceMenu.value === key ? '' : key;
};
// 设备参数弹窗与 SERIAL 栏共用同一份典型波特率，避免两处列表走样
const deviceBaudOptions = baudOptions;
const deviceStopBitsOptions = [
  { label: '1', value: 1 },
  { label: '1.5', value: 15 },
  { label: '2', value: 2 }
];
const deviceStopBitsLabel = (v: number) =>
  deviceStopBitsOptions.find(opt => opt.value === v)?.label ?? String(v);

// 老配置或尚未实现的 ini 键会让字段缺失：统一补齐缺省值，避免界面出现 undefined
const withDeviceDefaults = (d: Partial<PwrDevice>): PwrDevice => ({
  name: d.name ?? '',
  port: d.port ?? '',
  baud: Number(d.baud) > 0 ? Number(d.baud) : 9600,
  dataBits: Number(d.dataBits) > 0 ? Number(d.dataBits) : 8,
  stopBits: normalizeStopBits(d.stopBits),
  parity: d.parity || 'None',
  powerOn: d.powerOn ?? '',
  powerOff: d.powerOff ?? '',
  powerOnHex: d.powerOnHex !== false,
  powerOnEnter: d.powerOnEnter === true,
  powerOffHex: d.powerOffHex !== false,
  powerOffEnter: d.powerOffEnter === true
});

const pwrDevices = ref<PwrDevice[]>([]);
const selectedDeviceName = ref('');
const pwrDevPath = ref('');
const selectedDevice = computed(
  () => pwrDevices.value.find(d => d.name === selectedDeviceName.value) || null
);

// 启动时读取 pwr_dev.ini（文件不存在时由 Go 侧自动创建模板）
const loadPwrDevices = async () => {
  try {
    const list = await GetPwrDevices();
    pwrDevices.value = (list ?? []).map(d => withDeviceDefaults(d as unknown as Partial<PwrDevice>));
    pwrDevPath.value = await GetPwrDevPath();
    if (pwrDevices.value.length > 0 && !pwrDevices.value.some(d => d.name === selectedDeviceName.value)) {
      selectedDeviceName.value = pwrDevices.value[0].name;
    }
  } catch (e) {
    console.error('读取 pwr_dev.ini 失败:', e);
  }
};

// --- TASK：设备参数编辑（新增 / 删除 / 保存 / 取消，保存后立刻写回 pwr_dev.ini）---
const showDeviceEditor = ref(false);
const deviceDraft = ref<PwrDevice[]>([]);
const deviceDraftIndex = ref(0);
const deviceEditorError = ref('');
const deviceSaving = ref(false);

const openDeviceEditor = () => {
  deviceDraft.value = pwrDevices.value.map(d => ({ ...d }));
  deviceMenu.value = '';
  if (deviceDraft.value.length === 0) {
    deviceDraft.value = [emptyDeviceDraft()];
  }
  // 弹窗里的“当前选中”与设备选择框保持一致
  const idx = deviceDraft.value.findIndex(d => d.name === selectedDeviceName.value);
  deviceDraftIndex.value = idx >= 0 ? idx : 0;
  deviceEditorError.value = '';
  showDeviceEditor.value = true;
};

const addDeviceDraft = () => {
  deviceDraft.value.push(emptyDeviceDraft());
  deviceDraftIndex.value = deviceDraft.value.length - 1;
  deviceEditorError.value = '';
};

const removeDeviceDraft = () => {
  if (deviceDraft.value.length === 0) return;
  deviceDraft.value.splice(deviceDraftIndex.value, 1);
  deviceMenu.value = '';
  if (deviceDraft.value.length === 0) {
    deviceDraft.value = [emptyDeviceDraft()];
    deviceDraftIndex.value = 0;
  } else if (deviceDraftIndex.value >= deviceDraft.value.length) {
    deviceDraftIndex.value = deviceDraft.value.length - 1;
  }
  deviceEditorError.value = '';
};

const cancelDeviceEditor = () => {
  showDeviceEditor.value = false;
  deviceEditorError.value = '';
};

// PWR-ON / PWR-OFF 允许由多个十六进制数组成，中间用空格隔开，如 "0x01 0x02 AA"
const isHexTokenList = (text: string): boolean => {
  const trimmed = text.trim();
  if (trimmed === '') return true;
  return trimmed.split(/\s+/).every(token => /^(0x)?[0-9a-fA-F]{1,8}$/.test(token));
};

const saveDeviceEditor = async () => {
  const list: PwrDevice[] = [];
  const names = new Set<string>();
  for (let i = 0; i < deviceDraft.value.length; i++) {
    const row = deviceDraft.value[i];
    const name = row.name.trim();
    const port = row.port.trim();
    if (name === '') {
      deviceEditorError.value = `第 ${i + 1} 行的设备名称不能为空`;
      return;
    }
    if (names.has(name)) {
      deviceEditorError.value = `设备名称重复：${name}`;
      return;
    }
    names.add(name);
    if (row.powerOnHex && !isHexTokenList(row.powerOn)) {
      deviceEditorError.value = `${name} 的 PWR-ON 只能是十六进制数（多个用空格分隔），如 0x01 0x02 AA`;
      return;
    }
    if (row.powerOffHex && !isHexTokenList(row.powerOff)) {
      deviceEditorError.value = `${name} 的 PWR-OFF 只能是十六进制数（多个用空格分隔），如 0xAA 0x00`;
      return;
    }
    list.push(withDeviceDefaults({ ...row, name, port, powerOn: row.powerOn.trim(), powerOff: row.powerOff.trim() }));
  }

  deviceSaving.value = true;
  try {
    await SavePwrDevices(list as unknown as any);
    await loadPwrDevices();
    // 保存后，设备选择栏自动切到弹窗里“当前选中”的那台设备
    if (list.length === 0) {
      selectedDeviceName.value = '';
    } else {
      const picked = list[Math.min(deviceDraftIndex.value, list.length - 1)];
      if (picked && pwrDevices.value.some(d => d.name === picked.name)) {
        selectedDeviceName.value = picked.name;
      } else if (!pwrDevices.value.some(d => d.name === selectedDeviceName.value)) {
        selectedDeviceName.value = list[0].name;
      }
    }
    showDeviceEditor.value = false;
    deviceEditorError.value = '';
  } catch (e) {
    deviceEditorError.value = `保存失败：${String(e)}`;
  } finally {
    deviceSaving.value = false;
  }
};

// --- TASK：任务参数（task_config.ini）---
interface TaskConfigModel {
  saveLog: boolean;
  savePower: boolean;
  initialState: boolean;
  abnormalPause: boolean;
  waitSeconds: number;
  delaySeconds: number;
  runCount: number;
}
const taskConfig = ref<TaskConfigModel>({ saveLog: false, savePower: false, initialState: false, abnormalPause: false, waitSeconds: 5, delaySeconds: 1, runCount: 1 });
const taskCfgPath = ref('');

// TASK STATUS：运行时的实时状态（当前轮次 / 电源状态 / 当前电流）
// 数据来自后端 GetTaskRuntime()，运行期间每 200ms 轮询一次
interface TaskRuntimeModel {
  running: boolean;
  paused: boolean;
  // 暂停原因："abnormal" 表示有命令没跑完；空串表示没暂停
  pausedReason: string;
  round: number;
  runCount: number;
  normalCount: number;
  powerOn: boolean;
  current: string;
  phase: string;
  countdown: number;
  countdownTo: string;
  cmdIndex: number;
  cmdPhase: string;
  cmdCountdown: number;
  logPath: string;
  csvPath: string;
  message: string;
}
const emptyRuntime = (): TaskRuntimeModel => ({
  running: false,
  paused: false,
  pausedReason: '',
  round: 0,
  runCount: 0,
  normalCount: 0,
  powerOn: false,
  current: '',
  phase: 'idle',
  countdown: 0,
  countdownTo: '',
  cmdIndex: -1,
  cmdPhase: '',
  cmdCountdown: 0,
  logPath: '',
  csvPath: '',
  message: ''
});
const taskRt = ref<TaskRuntimeModel>(emptyRuntime());
const taskStatus = reactive({ round: 0, power: 'OFF' as 'ON' | 'OFF', current: null as number | null });

// 当前电流显示：NULL 或 “两位整数 + 一位小数”，例如 00.0 / 12.3
const formatCurrent = (): string => {
  if (taskStatus.current === null || !Number.isFinite(taskStatus.current)) return 'NULL';
  const tenths = Math.round(Math.max(0, Number(taskStatus.current)) * 10);
  const int = Math.floor(tenths / 10);
  const dec = tenths % 10;
  return `${String(int).padStart(2, '0')}.${dec}`;
};

const loadTaskConfig = async () => {
  try {
    const cfg = await GetTaskConfig();
    if (cfg) {
      taskConfig.value = {
        saveLog: !!cfg.saveLog,
        // 当前版本“保存功耗”固定为关（界面已置灰），旧配置里的 1 也按关处理
        savePower: false,
        initialState: !!cfg.initialState,
        abnormalPause: !!(cfg as any).abnormalPause,
        waitSeconds: Number(cfg.waitSeconds ?? 5),
        delaySeconds: Number(cfg.delaySeconds ?? 1),
        runCount: Number(cfg.runCount ?? 1)
      };
    }
    taskCfgPath.value = await GetTaskCfgPath();
  } catch (e) {
    console.error('读取 task_config.ini 失败:', e);
  }
};

// 开 / 关开关（保存日志、保存功耗、初始状态、异常暂停）点一下即翻转并落盘
type TaskFlagKey = 'saveLog' | 'savePower' | 'initialState' | 'abnormalPause';
const toggleTaskFlag = async (key: TaskFlagKey) => {
  if (taskRt.value.running) return;
  if (key === 'savePower') return; // 当前版本固定为关，不允许修改
  taskConfig.value[key] = !taskConfig.value[key];
  await persistTaskConfig();
};

// 勾选框 / 数字改动后立即落盘
const persistTaskConfig = async () => {
  try {
    await SaveTaskConfig(taskConfig.value as unknown as any);
    taskCfgPath.value = await GetTaskCfgPath();
  } catch (e) {
    showModal('保存任务参数失败', String(e), 'error');
  }
};

// 点击数字 → 弹窗编辑（保存 / 取消）
type TaskNumberKey = 'waitSeconds' | 'delaySeconds' | 'runCount';
const numberDialog = reactive({
  show: false,
  key: 'waitSeconds' as TaskNumberKey,
  title: '',
  unit: '秒',
  value: 0,
  min: 0,
  max: 86400
});

const openNumberDialog = (key: TaskNumberKey) => {
  const meta: Record<TaskNumberKey, { title: string; unit: string; min: number; max: number }> = {
    waitSeconds: { title: '运行时间', unit: '秒', min: 0, max: 86400 },
    delaySeconds: { title: '间隔时间', unit: '秒', min: 0, max: 86400 },
    runCount: { title: '运行次数', unit: '次', min: 1, max: 100000 }
  };
  numberDialog.key = key;
  numberDialog.title = meta[key].title;
  numberDialog.unit = meta[key].unit;
  numberDialog.min = meta[key].min;
  numberDialog.max = meta[key].max;
  numberDialog.value = Number(taskConfig.value[key]);
  numberDialog.show = true;
};

const confirmNumberDialog = async () => {
  let value = Math.floor(Number(numberDialog.value));
  if (!Number.isFinite(value)) value = numberDialog.min;
  if (value < numberDialog.min) value = numberDialog.min;
  if (value > numberDialog.max) value = numberDialog.max;
  numberDialog.value = value;
  (taskConfig.value as any)[numberDialog.key] = value;
  numberDialog.show = false;
  await persistTaskConfig();
};

const cancelNumberDialog = () => {
  numberDialog.show = false;
};

// --- 右侧栏：AUTO CMD（命令列表，存于 exe 同目录的 auto_cmd.txt，改动即时写盘）---
const autoCmds = ref<AutoCmdModel[]>([]);
const autoCmdSelected = ref(-1);
const autoCmdPath = ref('');
const autoRunning = ref(false);
type AutoCmdType = 'TX' | 'RX';
interface AutoCmdModel { type: AutoCmdType; number: number; text: string; hex: boolean; enter: boolean; }
// hex / enter 缺省为 false / true：只对 TX 有意义（RX 是文本包含匹配，两个开关会回落默认值），
// 读写时必须原样带上，否则一次保存就会把 auto_cmd.txt 里的 hex=/enter= 关键字抹掉。
const autoCmdDialog = reactive({
  show: false, index: -1, text: '', type: 'TX' as AutoCmdType,
  numberText: '00', numberError: '', hex: false, enter: true
});
const autoCmdInputRef = ref<HTMLTextAreaElement | null>(null);
const autoCmdNumberRef = ref<HTMLInputElement | null>(null);
// 行尾数字只保留两位（00 ~ 99）
const CMD_NUMBER_MAX = 99;

// 数字显示：个位数就只显示一位（7 → "7"），两位数照旧（42 → "42"）
const formatCmdNumber = (value: number): string => String(clampCmdNumber(value));

const loadAutoCmds = async () => {
  try {
    const list = await GetAutoCmds();
    autoCmds.value = ((list ?? []) as unknown as AutoCmdModel[]).map(c => ({
      type: (String(c?.type ?? 'TX').toUpperCase() === 'RX' ? 'RX' : 'TX') as AutoCmdType,
      number: clampCmdNumber(Number(c?.number ?? 0)),
      text: String(c?.text ?? ''),
      hex: !!c?.hex,
      enter: c?.enter !== false
    }));
    autoCmdPath.value = await GetAutoCmdPath();
    if (autoCmds.value.length === 0) autoCmdSelected.value = -1;
    else if (autoCmdSelected.value >= autoCmds.value.length) autoCmdSelected.value = autoCmds.value.length - 1;
  } catch (e) {
    console.error('读取 auto_cmd.txt 失败:', e);
  }
};

const persistAutoCmds = async () => {
  try {
    await SaveAutoCmds(autoCmds.value as unknown as any);
  } catch (e) {
    console.error('保存 auto_cmd.txt 失败:', e);
  }
};

const openAutoCmdDialog = (index: number) => {
  autoCmdDialog.index = index;
  const cmd = index >= 0 && index < autoCmds.value.length ? autoCmds.value[index] : null;
  autoCmdDialog.text = cmd ? cmd.text : '';
  autoCmdDialog.type = cmd ? cmd.type : 'TX';
  autoCmdDialog.numberText = formatCmdNumber(cmd ? cmd.number : 0);
  autoCmdDialog.numberError = '';
  // 原样回填行里的值：RX 行后端从不写 hex=/enter=，显示时由 dialogEnterOn / hexChecked 伪装成默认值
  autoCmdDialog.hex = !!cmd && !!cmd.hex;
  autoCmdDialog.enter = !cmd || cmd.enter !== false;
  autoCmdDialog.show = true;
  nextTick(() => autoCmdInputRef.value?.focus());
};

// 弹窗里点 TX / RX 标志即切换类型。HEX / 回车 只决定 TX 怎么发，切换时**不动**已存的值，
// 这样 TX → RX → TX 不会把一条十六进制命令退化成文本命令；RX 下开关只是置灰展示（见 dialogEnterOn），
// 真正归一化成默认值只在保存时做。
const toggleDialogCmdType = () => {
  autoCmdDialog.type = autoCmdDialog.type === 'TX' ? 'RX' : 'TX';
};

// HEX 预览：规则与后端 encodeCmdPayload 一致——空格分隔，每段 1~2 位十六进制（可带 0x），
// 统一补成两位大写；回车开着且末字节不是 CR/LF 时，实际发送还会再补一个 0D，预览里一并显示出来。
// 内容为空时 bytes 为空、仍算合法（清空后保存等于删掉这一条）。
const hexPreview = computed(() => {
  const raw = autoCmdDialog.text.trim();
  if (raw === '') return { bytes: '', valid: true, autoCr: false };
  const bytes: string[] = [];
  for (const field of raw.split(/\s+/)) {
    const clean = field.replace(/^0[xX]/, '');
    if (!/^[0-9a-fA-F]{1,2}$/.test(clean)) return { bytes: '', valid: false, autoCr: false };
    bytes.push(clean.toUpperCase().padStart(2, '0'));
  }
  const last = bytes[bytes.length - 1];
  return { bytes: bytes.join(' '), valid: true, autoCr: autoCmdDialog.enter && last !== '0D' && last !== '0A' };
});

// HEX / 回车 只对 TX 生效；HEX 打开但内容不是合法十六进制时禁止保存
const hexChecked = computed(() => autoCmdDialog.type === 'TX' && autoCmdDialog.hex);
const hexInvalid = computed(() => hexChecked.value && !hexPreview.value.valid);
// 回车开关在 RX 下只做展示：显示回落默认的「开」，但不改 autoCmdDialog.enter（切回 TX 时原值还在）
const dialogEnterOn = computed(() => (autoCmdDialog.type === 'RX' ? true : autoCmdDialog.enter));

// 功能键标记：点一下插入到光标处，后端发送前会替换成实际按键字节
const cmdKeyButtons = [
  { label: 'Enter', token: '{ENTER}', tip: '回车 CR（0x0D）' },
  { label: 'LF', token: '{LF}', tip: '换行 LF（0x0A）' },
  { label: 'Tab', token: '{TAB}', tip: '制表符（0x09）' },
  { label: 'Esc', token: '{ESC}', tip: 'ESC（0x1B）' },
  { label: '↑', token: '{UP}', tip: '上箭头（ESC [ A）' },
  { label: '↓', token: '{DOWN}', tip: '下箭头（ESC [ B）' },
  { label: '←', token: '{LEFT}', tip: '左箭头（ESC [ D）' },
  { label: '→', token: '{RIGHT}', tip: '右箭头（ESC [ C）' },
  { label: 'BS', token: '{BACKSPACE}', tip: '退格（0x7F）' },
  { label: 'Del', token: '{DEL}', tip: 'Delete（ESC [ 3 ~）' },
  { label: 'Home', token: '{HOME}', tip: 'Home（ESC [ H）' },
  { label: 'End', token: '{END}', tip: 'End（ESC [ F）' },
  { label: 'PgUp', token: '{PGUP}', tip: 'PageUp（ESC [ 5 ~）' },
  { label: 'PgDn', token: '{PGDN}', tip: 'PageDown（ESC [ 6 ~）' },
  { label: 'Space', token: '{SPACE}', tip: '空格' }
];

const insertCmdKey = (token: string) => {
  const el = autoCmdInputRef.value;
  if (!el) {
    autoCmdDialog.text += token;
    return;
  }
  const start = el.selectionStart ?? autoCmdDialog.text.length;
  const end = el.selectionEnd ?? start;
  autoCmdDialog.text = autoCmdDialog.text.slice(0, start) + token + autoCmdDialog.text.slice(end);
  nextTick(() => {
    const pos = start + token.length;
    el.focus();
    el.setSelectionRange(pos, pos);
  });
};

// 导入：弹文件选择框，选中后覆盖当前列表（导出入口已按需求移除，后端接口仍保留）
const importAutoCmds = async () => {
  try {
    const count = await ImportAutoCmds();
    if (count === '' || count === undefined) return; // 用户取消
    await loadAutoCmds();
    autoCmdSelected.value = -1;
    showModal('导入成功', `已导入 ${count} 条命令，覆盖了当前列表。`, 'success');
  } catch (e) {
    showModal('导入失败', String(e), 'error');
  }
};

// 新增：有选中就插入到该条**上方**，否则追加到末尾；插入后立即打开编辑窗口
const addAutoCmd = () => {
  const at = autoCmdSelected.value >= 0 ? autoCmdSelected.value : autoCmds.value.length;
  autoCmds.value.splice(at, 0, { type: 'TX', number: 0, text: '', hex: false, enter: true });
  autoCmdSelected.value = at;
  openAutoCmdDialog(at);
};

const removeAutoCmd = () => {
  if (autoCmdSelected.value < 0 || autoCmdSelected.value >= autoCmds.value.length) return;
  autoCmds.value.splice(autoCmdSelected.value, 1);
  if (autoCmds.value.length === 0) autoCmdSelected.value = -1;
  else if (autoCmdSelected.value >= autoCmds.value.length) autoCmdSelected.value = autoCmds.value.length - 1;
  persistAutoCmds();
};

const confirmAutoCmdDialog = async () => {
  const idx = autoCmdDialog.index;
  // 数字超过两位：提示有误，不允许保存
  if (!cmdNumberValid()) {
    autoCmdDialog.numberError = '只能输入两位数字（00 ~ 99）';
    return;
  }
  // HEX 开着但内容不是合法十六进制：拦下保存，否则任务跑到这一条会发不出去
  if (hexInvalid.value) return;
  const text = autoCmdDialog.text.replace(/[\r\n]+/g, ' ').trim();
  const number = clampCmdNumber(Number(autoCmdDialog.numberText || '0'));
  // RX 是文本包含匹配，hex / enter 无意义，保存时强制回落默认值
  const isTx = autoCmdDialog.type === 'TX';
  const hex = isTx && autoCmdDialog.hex;
  const enter = isTx ? autoCmdDialog.enter : true;
  if (idx >= 0 && idx < autoCmds.value.length) {
    if (text === '') autoCmds.value.splice(idx, 1);
    else autoCmds.value[idx] = { type: autoCmdDialog.type, number, text, hex, enter };
  }
  if (autoCmds.value.length === 0) autoCmdSelected.value = -1;
  else if (autoCmdSelected.value >= autoCmds.value.length) autoCmdSelected.value = autoCmds.value.length - 1;
  autoCmdDialog.show = false;
  await persistAutoCmds();
};

// 数字只能是 0..99 的整数
const clampCmdNumber = (value: number): number => {
  let n = Math.floor(Number(value));
  if (!Number.isFinite(n) || n < 0) n = 0;
  if (n > CMD_NUMBER_MAX) n = CMD_NUMBER_MAX;
  return n;
};

// 弹窗里数字框的校验：只允许 0~2 位数字
const cmdNumberValid = (): boolean => autoCmdDialog.numberText.length <= 2 && /^\d*$/.test(autoCmdDialog.numberText);

// 输入时过滤非数字字符；超过两位就给出提示（不截断，方便用户看到自己多输了）
const onCmdNumberInput = (e: Event) => {
  const el = e.target as HTMLInputElement;
  const digits = (el.value || '').replace(/[^\d]/g, '');
  if (digits !== el.value) el.value = digits;
  autoCmdDialog.numberText = digits;
  autoCmdDialog.numberError = digits.length > 2 ? '只能输入两位数字（00 ~ 99）' : '';
};

// 数字框按回车 = 直接保存
const onCmdNumberEnter = () => {
  confirmAutoCmdDialog();
};

// 取消编辑：刚新增的空行直接丢掉，避免留下空条目
const cancelAutoCmdDialog = () => {
  const idx = autoCmdDialog.index;
  autoCmdDialog.show = false;
  if (idx >= 0 && idx < autoCmds.value.length && autoCmds.value[idx].text.trim() === '') {
    autoCmds.value.splice(idx, 1);
    if (autoCmds.value.length === 0) autoCmdSelected.value = -1;
    else if (autoCmdSelected.value >= autoCmds.value.length) autoCmdSelected.value = autoCmds.value.length - 1;
    persistAutoCmds();
  }
};

// 启动 / 停止任务：真正的执行在后端 task.go 里，这里只发起请求并轮询状态
const taskLocked = computed(() => taskRt.value.running);
// 只有串口已连接才能启动任务；连接类型是否真的是 SERIAL 由后端 StartTask 再校验一次
const canStartTask = computed(() => isConnected.value);

let taskPollTimer: number | null = null;
const stopTaskPolling = () => {
  if (taskPollTimer !== null) {
    window.clearInterval(taskPollTimer);
    taskPollTimer = null;
  }
};

const syncTaskStatus = () => {
  taskStatus.round = taskRt.value.round;
  taskStatus.power = taskRt.value.powerOn ? 'ON' : 'OFF';
  // 当前版本不采集电流：固定显示 NULL（电流解析仍在后端，只是界面不展示）
  taskStatus.current = null;
};

const pollTaskRuntime = async () => {
  try {
    const rt = await GetTaskRuntime();
    taskRt.value = { ...emptyRuntime(), ...(rt as unknown as TaskRuntimeModel) };
    autoRunning.value = taskRt.value.running;
    syncTaskStatus();
    if (!taskRt.value.running) stopTaskPolling();
  } catch (e) {
    console.error('读取任务状态失败:', e);
  }
};

const startTaskPolling = () => {
  stopTaskPolling();
  taskPollTimer = window.setInterval(pollTaskRuntime, 200);
};

// TX 命令发送时追加的换行，跟随 TX EDITOR 的 EOL 设置
const taskEol = (): string => (lineEndingMode.value === 'LF' ? '\n' : lineEndingMode.value === 'NONE' ? '' : '\r\n');

const toggleAutoRun = async () => {
  if (taskRt.value.running) {
    const res = await StopTask();
    if (res) showModal('停止失败', res, 'error');
    await pollTaskRuntime();
    return;
  }
  const res = await StartTask({ deviceName: selectedDeviceName.value, eol: taskEol() });
  if (res) {
    showModal('无法启动任务', res, 'error');
    return;
  }
  // 启动即清掉列表选中：选中态只用于「新增插入位置 / 删除目标」，
  // 任务跑起来以后还挂着一行高亮，既没有用处，也容易让人以为那行正在被单独执行。
  autoCmdSelected.value = -1;
  await pollTaskRuntime();
  startTaskPolling();
};

// 异常暂停后的两条出路：结束任务（掉电并收工） / 跳过本轮（掉电，等间隔后继续下一轮）
// 点下后立刻置忙：Go 侧投递完决定就返回，要等下一个 200ms 轮询才变非暂停，
// 不挡住第二次点击就会拿回「当前不在异常暂停状态」并弹一个假错误。
const taskBusy = ref(false);
const endTask = async () => {
  if (taskBusy.value) return;
  taskBusy.value = true;
  try {
    const res = await EndTask();
    if (res) showModal('结束任务失败', res, 'error');
    await pollTaskRuntime();
  } finally {
    taskBusy.value = false;
  }
};

const skipRound = async () => {
  if (taskBusy.value) return;
  taskBusy.value = true;
  try {
    const res = await SkipRound();
    if (res) showModal('跳过本轮失败', res, 'error');
    await pollTaskRuntime();
  } finally {
    taskBusy.value = false;
  }
};

// 网络参数

// --- 2. 数据处理 ---
const receivedData = ref<string>('');
// 使用 shallowRef，这样 Vue 不会深度监听数组内部的每一个数字
const rawDataBuffer = shallowRef<number[]>([]);

// 把收到的原始文本按“终端”语义写进显示缓冲：
//   \b(S) 退格 → 删掉上一个字符（下位机回显 "\b \b" 就变成真正的删字符）
//   \r    回车 → 与后面的 \n 合并成一个换行，单独的 \r 也当换行
//   其它不可打印控制字符直接丢弃，避免在文本框里显示成方框/乱码
const applyTerminalChars = (buffer: string, chunk: string): string => {
  let out = buffer;
  for (let i = 0; i < chunk.length; i++) {
    const ch = chunk[i];
    const code = ch.charCodeAt(0);
    if (ch === '\b') {
      if (out.length > 0 && out[out.length - 1] !== '\n') out = out.slice(0, -1);
      continue;
    }
    if (ch === '\r') {
      if (chunk[i + 1] === '\n') continue; // 交给后面的 \n 落行
      out += '\n';
      continue;
    }
    if (code < 0x20 && ch !== '\n' && ch !== '\t') continue;
    out += ch;
  }
  return out;
};

// 注意：使用 shallowRef 后，push 不会触发视图更新，
// 但因为你的 rawDataBuffer 主要是给 watch(showHex) 用的，
// 而 receivedData 才是直接绑定的视图，所以这通常没问题。
// 如果必须触发更新，赋值操作 rawDataBuffer.value = ... 会触发。
const sendInput = ref('');
// 默认关闭 Hex 显示
const showHex = ref(false);
// Hex 发送状态
const hexSend = ref(false);

// 行尾符配置（默认 CRLF：TX EDITOR 回车时发送 \r\n）
const lineEndingMode = ref<'NONE' | 'LF' | 'CRLF'>('CRLF');
const showEolDropdown = ref(false);

const eolOptions = [
  { label: 'None', value: 'NONE' },
  { label: '\\n (LF)', value: 'LF' },
  { label: '\\r\\n (CRLF)', value: 'CRLF' }
];

const currentEolLabel = computed(() =>
    eolOptions.find(o => o.value === lineEndingMode.value)?.label || 'None'
);

const selectEol = (val: 'NONE' | 'LF' | 'CRLF') => {
  lineEndingMode.value = val;
  showEolDropdown.value = false;
};

// 自定义下拉框状态管理
const showPortDropdown = ref(false);
const showBaudDropdown = ref(false);
const showDataBitsDropdown = ref(false);
const showParityDropdown = ref(false);
const showStopBitsDropdown = ref(false);

// 波特率下拉项：常用值之外，如果当前值不属于列表（旧配置或设备参数里自填的），补一项进去，
// 否则换回下拉后当前速率会"凭空消失"，用户也无从选回原值。
const baudMenuOptions = computed(() => {
  const list = [...baudOptions];
  if (!list.includes(baudRate.value)) list.push(baudRate.value);
  return list.sort((a, b) => a - b);
});
// TASK 页的“设备选择”下拉
const showDeviceDropdown = ref(false);

// 布局状态：TX EDITOR 默认收起，由底部细长按钮控制展开 / 收起（按钮始终固定在底部）
const showTxEditor = ref(false);
// TX EDITOR 输入框的历史记录（↑ / ↓ 翻看）
const txHistory = ref<string[]>([]);
const txHistoryIndex = ref(-1);
const txInputRef = ref<HTMLInputElement | null>(null);
// RX MONITOR 终端式输入的历史命令 + 当前行缓冲（↑/↓ 翻历史时用于擦除并重放）
const rxHistory = ref<string[]>([]);
const rxHistoryIndex = ref(-1);
const rxLineBuffer = ref('');

const dataBitsOptions = [8, 7, 6, 5];
const parityOptions = [
  { label: 'None', value: 'None' },
  { label: 'Odd', value: 'Odd' },
  { label: 'Even', value: 'Even' },
  { label: 'Mark', value: 'Mark' },
  { label: 'Space', value: 'Space' }
];
const stopBitsOptions = [1, 1.5, 2];

const autoScroll = ref(true);
const logWindowRef = ref<HTMLElement | null>(null);
const rxCount = ref(0);
const txCount = ref(0);

// --- RX 行视图（与 receivedData 的终端缓冲并行）---
// 行视图只负责"一行一条"的结构化展示：RX 按 \n 切行，TX（任务发出的命令）另起一行并打时间戳。
// 它与下面的 receivedData 管道互不替代，Hex 显示仍然看 receivedData。
interface LogLine { id: number; dir: 'rx' | 'tx'; text: string; time: string }
const MAX_LOG_LINES = 2000;
const transcript = ref<LogLine[]>([]);
// 单调递增的行号：到 2000 行上限开始裁剪时 :key 不会整体错位（否则整表每行都要重新 patch）
let logSeq = 0;
// 还没收到 \n 的这一行用「字符数组 + 光标」表示：终端里退格（\b / 0x7F）要真的删字符、
// 回车（\r）要把光标拉回行首覆盖本行，纯字符串表达不了这两种改写
let rxLineChars: string[] = [];
let rxLineCursor = 0;
// 这条未换行的 RX 行显示在 transcript 的哪一行上（-1 = 当前没有未换行的行）
let rxOpenIdx = -1;
// 单行最长字符数：下位机长时间不换行时只保留尾部，避免一条行无限增长拖垮渲染
const MAX_LOG_LINE_CHARS = 4096;
// 行视图用自己的解码器：receivedData 那支是流式状态（{stream:true}），两边共用一个会互相推进多字节状态
const lineDecoder = new TextDecoder('utf-8');
// 接收区空态提示：行视图是 div，用不了 ::placeholder，Hex 的 textarea 复用同一句话
const RX_HINT = '点击这里后直接键入即可发送（Enter 发回车、↑/↓ 翻历史、Backspace 发 0x7F、Esc/Delete/←/→/Home/End/PgUp/PgDn 发转义序列、Ctrl+字母 发 0x01–0x1A；选中文本可 Ctrl+C 复制、Ctrl+V 直接发给下位机）';

// 功能键按 xterm 的转义序列原样发出：下位机的 shell（letter-shell 等）认这一套
const ESC_KEYS: Record<string, string> = {
  Escape: '\x1b',
  Delete: '\x1b[3~',
  ArrowLeft: '\x1b[D',
  ArrowRight: '\x1b[C',
  Home: '\x1b[H',
  End: '\x1b[F',
  PageUp: '\x1b[5~',
  PageDown: '\x1b[6~',
};

const nowHms = (): string => new Date().toTimeString().slice(0, 8);

// 超长行只保留尾部：完整行和未换行的尾巴都走这里，避免单行无限增长拖垮渲染
const clampLineText = (text: string): string =>
  text.length > MAX_LOG_LINE_CHARS ? text.slice(-MAX_LOG_LINE_CHARS) : text;

const appendLogLine = (dir: 'rx' | 'tx', text: string, time = '') => {
  transcript.value.push({ id: ++logSeq, dir, text: clampLineText(text), time });
  if (transcript.value.length > MAX_LOG_LINES) {
    const dropped = transcript.value.length - MAX_LOG_LINES;
    transcript.value.splice(0, dropped);
    // 超长裁剪会挪动下标：未换行的行跟着前移，被裁掉时视为已收尾
    if (rxOpenIdx >= 0) rxOpenIdx = Math.max(-1, rxOpenIdx - dropped);
  }
};

const rxLineText = (): string => rxLineChars.join('');

// 未换行的行先挂到 transcript 上（下位机提示符常常不带换行），后续 chunk 就地改写这一行
const syncRxPendingLine = () => {
  // 一直不换行时只保留尾部，避免这一行无限增长拖垮渲染；光标跟着前移
  if (rxLineChars.length > MAX_LOG_LINE_CHARS) {
    const dropped = rxLineChars.length - MAX_LOG_LINE_CHARS;
    rxLineChars.splice(0, dropped);
    rxLineCursor = Math.max(0, rxLineCursor - dropped);
  }
  const text = clampLineText(rxLineText());
  if (rxOpenIdx >= 0) {
    // 退格删空时也要同步成空行，否则屏幕上留着已经删掉的字符
    transcript.value[rxOpenIdx].text = text;
  } else if (text) {
    appendLogLine('rx', text);
    rxOpenIdx = transcript.value.length - 1;
  }
};

// 收到 \n：把当前行收尾。已经显示过的按行号改写，从没显示过的补一行（空行也要保留）
const finishRxLine = () => {
  const text = clampLineText(rxLineText());
  if (rxOpenIdx >= 0) {
    transcript.value[rxOpenIdx].text = text;
  } else {
    appendLogLine('rx', text);
  }
  rxOpenIdx = -1;
  rxLineChars = [];
  rxLineCursor = 0;
};

// 行视图必须按终端语义解释字节流：下位机回显退格时发的是 \b 或 0x7F，若原样塞进文本，
// 光标回不到上一格，表现就是"按下退格没反应"。规则：
//   \n 收尾换行；\r 光标回行首（后续字符覆盖本行，进度条刷新靠这个）；
//   \b / 0x7F 删掉光标前一个字符；其余控制字符（含 ESC 序列）不参与显示。
const pushRxChunk = (chunk: string) => {
  if (!chunk) return;
  for (const ch of chunk) {
    if (ch === '\n') {
      finishRxLine();
      continue;
    }
    if (ch === '\r') {
      rxLineCursor = 0;
      continue;
    }
    if (ch === '\b' || ch === '\u007f') {
      if (rxLineCursor > 0) {
        rxLineCursor -= 1;
        rxLineChars.splice(rxLineCursor, 1);
      }
      continue;
    }
    const code = ch.codePointAt(0) ?? 0;
    if (code < 0x20 || code === 0x7f) continue;
    if (rxLineCursor < rxLineChars.length) {
      rxLineChars[rxLineCursor] = ch;
    } else {
      rxLineChars.push(ch);
    }
    rxLineCursor += 1;
  }
  syncRxPendingLine();
  // 滚屏交给调用方：一批数据处理完只滚一次，这里不重复排 nextTick
};

const pushTxLine = (text: string, time?: string) => {
  // 未换行的 RX 行原样留在上面（只是后面多了这一条 TX），后面的 RX 继续按行号续写它，
  // 所以这里不用动 pending，也不会把同一段文字显示两遍
  appendLogLine('tx', text, time || nowHms());
  scrollLogToEnd();
};

// 滚到底：只在"自动滚屏"打开时生效，等 DOM 更新完再设 scrollTop
const scrollLogToEnd = () => {
  if (!autoScroll.value) return;
  nextTick(() => {
    if (logWindowRef.value) logWindowRef.value.scrollTop = logWindowRef.value.scrollHeight;
  });
};

// --- Charting State ---
const showChart = ref(false);
// We'll store series data here. Map<seriesIndex, [timestamp, value][]>
const chartData = reactive<{ [key: number]: number[][] }>({});

// Use sample counter for X axis to prevent timestamp clumping
const sampleCount = ref(0);


const chartOption = computed(() => {
  const seriesIndices = Object.keys(chartData);
  const seriesList = seriesIndices.map(idx => ({
    name: `Ch ${idx}`,
    type: 'line',
    showSymbol: false,
    sampling: 'lttb', // Optimize for high density
    data: chartData[parseInt(idx)]
  }));


  return {
    backgroundColor: 'transparent',
    tooltip: {
      trigger: 'axis'
    },
    legend: {
        show: true,
        textStyle: { color: theme.textMain }
    },
    grid: {
      top: 30, left: 50, right: 20, bottom: 30
    },
    xAxis: {
      type: 'value', // Changed from 'time' to 'value' for sample-based plotting
      splitLine: { show: false },
       axisLabel: { color: theme.textSub }
    },

    yAxis: {
      type: 'value',
      splitLine: { show: true, lineStyle: { color: theme.bgSide } },
       axisLabel: { color: theme.textSub }
    },
    dataZoom: [
        { type: 'inside' },
        { type: 'slider', height: 20, bottom: 0 }
    ],
    series: seriesList.length > 0 ? seriesList : [{ type: 'line', data: [] }]
  };
});

// Binary parser buffer
let parserBuffer: number[] = [];
const HEX_PACKET_SUFFIX = [0x00, 0x00, 0x80, 0x7f];

// Function to parse incoming data for plotting
const parseAndPlot = (newBytes: number[]) => {
    if (!showChart.value) return;

    // 1. String Protocol: &DRAW,{float}...#\n
    // We'll use a crude string check on the new bytes for now. 
    // Ideally we should buffer strings too, but for simplicity assuming atomic packets or simple stream.
    // Let's convert to string to check for &DRAW
    const strChunk = new TextDecoder().decode(new Uint8Array(newBytes));
    // Use regex to find all matches in this chunk (and potentially across chunks, but let's stick to chunk for now or append to a small string buffer if needed)
    // To handle split packets, we might need a string buffer.
    // For safety, let's just match within the chunk or a sliding window?
    // User requirement: "&DRAW,{float1},{float2}#\n"
    
    // Simple String Parser
    const drawMatches = strChunk.matchAll(/&DRAW,([\d.,-]+)#/g);
    for (const match of drawMatches) {
        // match[1] is "float1,float2"
        const parts = match[1].split(',');
        const xVal = sampleCount.value++; // Increment sample counter
        
        parts.forEach((valStr, idx) => {
            const val = parseFloat(valStr);
            if (!isNaN(val)) {
                 if (!chartData[idx]) chartData[idx] = [];
                 chartData[idx].push([xVal, val]);
                 // Limit bounds
                 if (chartData[idx].length > 1000) chartData[idx].shift();
            }
        });
    }

    // 2. Hex Protocol: ... 0x00 0x00 0x80 0x7f

    // Append to buffer
    parserBuffer.push(...newBytes);
    
    // Look for suffix
    // We need to find the sequence 00 00 80 7f
    let searchIdx = 0;
    while (searchIdx <= parserBuffer.length - 4) {
        // Check for suffix
        if (parserBuffer[searchIdx] === 0x00 &&
            parserBuffer[searchIdx+1] === 0x00 &&
            parserBuffer[searchIdx+2] === 0x80 &&
            parserBuffer[searchIdx+3] === 0x7f) {
            
            // Found packet end.
            // The packet content is everything before this suffix (up to previous suffix or start)
            // But we process continuously.
            // How many floats? "Arbitrary count" logic isn't specified for Hex, but earlier user said "{float} can be arbitrary count" for string.
            // For Hex, usually it's fixed or described. 
            // Let's assume the payload BEFORE the suffix contains 32-bit floats.
            
            // Where did this packet start? 
            // We just take as many floats as possible from the bytes before this suffix.
            // Let's try to interpret the preceding bytes as floats.
            
            const endIndex = searchIdx;
            // Let's suppose the packet is just the floats + suffix.
            // We need to decide where the packet started. 
            // If we greedily consume, we might consume garbage.
            // But without a header, we can only assume the stream *is* floats.
            
            // Strategy: Backtrack from endIndex in 4-byte chunks.
            const payloadBytes = parserBuffer.slice(0, endIndex);
            const floatCount = Math.floor(payloadBytes.length / 4);

            
            const view = new DataView(new Uint8Array(payloadBytes).buffer);
            
            // We read from the end or start? 
            // Let's read all available valid floats.
            
            // Advance key counter for this packet
            const xVal = sampleCount.value++;

            for (let i = 0; i < floatCount; i++) {
                // Little Endian floats
                // offset = payloadBytes.length - (floatCount * 4) + (i * 4);
                
                // Let's simpler: payload is payloadBytes.
                const start = payloadBytes.length % 4; // Buffer alignment
                const val = view.getFloat32(start + i * 4, true); // Little Endian
                
                if (!chartData[i]) chartData[i] = [];
                chartData[i].push([xVal, val]);
                 if (chartData[i].length > 1000) chartData[i].shift();
            }
            
            // Remove processed part (up to searchIdx + 4)
            parserBuffer.splice(0, searchIdx + 4);
            searchIdx = 0; // Reset search
        } else {
            searchIdx++;
        }
    }
    
     // Cap buffer size
     if (parserBuffer.length > 4096) {
         parserBuffer.shift(); // Drop old byte
     }
}

// --- 3. UI 状态 (皮肤 & 弹窗) ---
// 一体化换肤：每套皮肤只改 CSS 变量与背景装饰，不改任何 DOM 结构与交互逻辑
const showSkinPanel = ref(false);

type ThemeType = {
  bgMain: string;
  bgSide: string;
  primary: string;
  textMain: string;
  textSub: string;
  error: string;
};

type SkinId = 'spatial' | 'aurora' | 'cyberpunk' | 'ai-native' | 'bento' | 'soft';

interface SkinDef {
  id: SkinId;
  name: string;
  desc: string;
  swatch: [string, string, string];
  theme: ThemeType;
  vars: Record<string, string>;
}

const SKINS: SkinDef[] = [
  {
    id: 'spatial',
    name: 'Bench',
    desc: '纸感台面 · 仪器绿（默认）',
    swatch: ['#E9E5DB', '#2F6B58', '#FFFFFF'],
    theme: { bgMain: '#F7F5EF', bgSide: '#E9E5DB', primary: '#2F6B58', textMain: '#23261F', textSub: '#6D6F64', error: '#A6493F' },
    vars: {
      '--card': '#FFFFFF',
      '--field': '#F3F0E8',
      '--hover': '#EFEBE1',
      '--line': 'rgba(35,38,31,0.12)',
      '--line-2': 'rgba(35,38,31,0.24)',
      '--accent-soft': 'rgba(47,107,88,0.10)',
      '--accent-ring': 'rgba(47,107,88,0.22)',
      '--shadow-xs': '0 1px 2px rgba(24,27,21,0.06)',
      '--shadow-md': '0 12px 30px rgba(24,27,21,0.12)',
      '--shadow-lg': '0 26px 64px rgba(24,27,21,0.20)',
      '--radius': '10px',
      '--blur': '0px',
      '--accent': '#7FA694',
      '--glow': 'rgba(47,107,88,0.28)'
    }
  },
  {
    id: 'aurora',
    name: 'Aurora',
    desc: '极光 · 紫青粉流动渐变',
    swatch: ['#F5F7FF', '#6E7BF2', '#5AD1C8'],
    theme: { bgMain: '#F5F7FF', bgSide: '#EEF1FE', primary: '#6E7BF2', textMain: '#3B4152', textSub: '#7C8497', error: '#E5677F' },
    vars: {
      '--card': 'rgba(255,255,255,0.90)',
      '--field': 'rgba(255,255,255,0.62)',
      '--hover': 'rgba(110,123,242,0.07)',
      '--line': 'rgba(110,120,220,0.16)',
      '--line-2': 'rgba(110,120,220,0.30)',
      '--accent-soft': 'rgba(110,123,242,0.10)',
      '--accent-ring': 'rgba(110,123,242,0.24)',
      '--shadow-xs': '0 1px 2px rgba(96,104,214,0.10)',
      '--shadow-md': '0 14px 40px rgba(96,104,214,0.18)',
      '--shadow-lg': '0 30px 70px rgba(96,104,214,0.24)',
      '--radius': '10px',
      '--blur': '20px',
      '--accent': '#8B7CFF',
      '--glow': 'rgba(139,124,255,0.35)'
    }
  },
  {
    id: 'cyberpunk',
    name: 'Cyberpunk',
    desc: '赛博朋克 · 霓虹青与洋红',
    swatch: ['#0B0F14', '#00E5FF', '#FF2E88'],
    theme: { bgMain: '#0B0F14', bgSide: '#0E141B', primary: '#00E5FF', textMain: '#E6F7FF', textSub: '#7FA0B2', error: '#FF2E88' },
    vars: {
      '--card': 'rgba(13,20,27,0.92)',
      '--field': 'rgba(0,229,255,0.05)',
      '--hover': 'rgba(0,229,255,0.09)',
      '--line': 'rgba(0,229,255,0.20)',
      '--line-2': 'rgba(0,229,255,0.42)',
      '--accent-soft': 'rgba(0,229,255,0.13)',
      '--accent-ring': 'rgba(0,229,255,0.30)',
      '--shadow-xs': '0 0 12px rgba(0,229,255,0.12)',
      '--shadow-md': '0 0 24px rgba(0,229,255,0.20)',
      '--shadow-lg': '0 0 60px rgba(255,46,136,0.24)',
      '--radius': '8px',
      '--blur': '10px',
      '--accent': '#FF2E88',
      '--glow': 'rgba(0,229,255,0.45)'
    }
  },
  {
    id: 'ai-native',
    name: 'AI-Native',
    desc: 'AI 原生 · 石墨底 + 紫色智能光',
    swatch: ['#0E1116', '#A78BFA', '#34D399'],
    theme: { bgMain: '#0E1116', bgSide: '#121722', primary: '#A78BFA', textMain: '#E8ECF4', textSub: '#8A94A8', error: '#FB7185' },
    vars: {
      '--card': 'rgba(22,28,38,0.94)',
      '--field': 'rgba(255,255,255,0.045)',
      '--hover': 'rgba(167,139,250,0.08)',
      '--line': 'rgba(255,255,255,0.09)',
      '--line-2': 'rgba(167,139,250,0.32)',
      '--accent-soft': 'rgba(167,139,250,0.12)',
      '--accent-ring': 'rgba(167,139,250,0.28)',
      '--shadow-xs': '0 2px 10px rgba(0,0,0,0.30)',
      '--shadow-md': '0 16px 40px rgba(0,0,0,0.45)',
      '--shadow-lg': '0 30px 80px rgba(0,0,0,0.55)',
      '--radius': '12px',
      '--blur': '18px',
      '--accent': '#34D399',
      '--glow': 'rgba(167,139,250,0.35)'
    }
  },
  {
    id: 'bento',
    name: 'Bento Grids',
    desc: '便当盒 · 白底大圆角硬边框',
    swatch: ['#FAFAF7', '#2F6F5E', '#E4A11B'],
    theme: { bgMain: '#FAFAF7', bgSide: '#F2F2EC', primary: '#2F6F5E', textMain: '#23262B', textSub: '#6B7078', error: '#D9534F' },
    vars: {
      '--card': '#FFFFFF',
      '--field': '#F7F7F2',
      '--hover': '#F1F1E9',
      '--line': 'rgba(20,24,28,0.12)',
      '--line-2': 'rgba(20,24,28,0.24)',
      '--accent-soft': 'rgba(47,111,94,0.10)',
      '--accent-ring': 'rgba(47,111,94,0.22)',
      '--shadow-xs': '0 1px 0 rgba(20,24,28,0.06)',
      '--shadow-md': '0 12px 30px rgba(20,24,28,0.12)',
      '--shadow-lg': '0 26px 60px rgba(20,24,28,0.16)',
      '--radius': '12px',
      '--blur': '8px',
      '--accent': '#E4A11B',
      '--glow': 'rgba(47,111,94,0.28)'
    }
  },
  {
    id: 'soft',
    name: 'Soft UI Evolution',
    desc: '新拟物 · 双层柔影浮起',
    swatch: ['#ECEEF3', '#6D8BA8', '#FFFFFF'],
    theme: { bgMain: '#ECEEF3', bgSide: '#E6E9F0', primary: '#6D8BA8', textMain: '#4A5160', textSub: '#8792A3', error: '#CF6679' },
    vars: {
      '--card': 'rgba(255,255,255,0.74)',
      '--field': 'rgba(255,255,255,0.52)',
      '--hover': 'rgba(255,255,255,0.90)',
      '--line': 'rgba(163,177,198,0.38)',
      '--line-2': 'rgba(140,158,180,0.50)',
      '--accent-soft': 'rgba(109,139,168,0.13)',
      '--accent-ring': 'rgba(109,139,168,0.26)',
      '--shadow-xs': '0 1px 2px rgba(163,177,198,0.30), 0 2px 8px rgba(255,255,255,0.60)',
      '--shadow-md': '8px 8px 24px rgba(163,177,198,0.40), -8px -8px 24px rgba(255,255,255,0.95)',
      '--shadow-lg': '12px 12px 40px rgba(163,177,198,0.45), -12px -12px 40px rgba(255,255,255,0.98)',
      '--radius': '12px',
      '--blur': '12px',
      '--accent': '#8FA8C0',
      '--glow': 'rgba(109,139,168,0.35)'
    }
  }
];

const SKIN_STORAGE_KEY = 'serialmatex.skin';
const isSkinId = (v: string): v is SkinId => SKINS.some((s) => s.id === v);
const readSavedSkin = (): SkinId => {
  try {
    const saved = localStorage.getItem(SKIN_STORAGE_KEY);
    if (saved && isSkinId(saved)) return saved;
  } catch { /* 本地存储不可用时退回默认皮肤 */ }
  return 'spatial';
};

const activeSkin = ref<SkinId>(readSavedSkin());
const currentSkin = computed<SkinDef>(() => SKINS.find((s) => s.id === activeSkin.value) ?? SKINS[0]);
const theme = reactive<ThemeType>({ ...currentSkin.value.theme });

// 主色上的文字：按主色亮度选深墨或白，保证亮色皮肤（霓虹青、浅紫）上的小字也够对比
const inkOn = (hex: string) => {
  const h = hex.replace('#', '');
  const full = h.length === 3 ? h.split('').map((c) => c + c).join('') : h;
  const lin = (c: number) => (c <= 0.03928 ? c / 12.92 : Math.pow((c + 0.055) / 1.055, 2.4));
  const r = lin(parseInt(full.slice(0, 2), 16) / 255);
  const g = lin(parseInt(full.slice(2, 4), 16) / 255);
  const b = lin(parseInt(full.slice(4, 6), 16) / 255);
  return 0.2126 * r + 0.7152 * g + 0.0722 * b > 0.22 ? '#0C1420' : '#FFFFFF';
};

const cssVars = computed(() => ({
  '--bg-main': theme.bgMain,
  '--bg-side': theme.bgSide,
  '--col-primary': theme.primary,
  '--text-main': theme.textMain,
  '--text-sub': theme.textSub,
  '--col-error': theme.error,
  '--on-primary': inkOn(theme.primary),
  '--on-error': inkOn(theme.error),
  ...currentSkin.value.vars
}));

// 一键换肤：切换皮肤 + 立即落盘，下次启动按保存的皮肤加载
const applySkin = (id: SkinId) => {
  activeSkin.value = id;
  Object.assign(theme, (SKINS.find((s) => s.id === id) ?? SKINS[0]).theme);
  try { localStorage.setItem(SKIN_STORAGE_KEY, id); } catch { /* 忽略存储失败 */ }
  showSkinPanel.value = false;
};

// 自定义弹窗状态
const modal = reactive({
  show: false,
  title: '',
  message: '',
  type: 'error' as 'error' | 'info' | 'success'
});

const showModal = (title: string, message: string, type: 'error' | 'info' | 'success' = 'error') => {
  modal.title = title;
  modal.message = message;
  modal.type = type;
  modal.show = true;
};

const closeModal = () => {
  modal.show = false;
};

// --- Update 相关状态 ---
const showAboutPanel = ref(false);
const appVersion = ref('');
const updateInfo = reactive({
  checking: false,
  available: false,
  currentVersion: '',
  latestVersion: '',
  releaseNotes: '',
  downloadUrl: '',
  assetSize: 0
});
const updateProgress = reactive({
  downloading: false,
  progress: 0,
  downloaded: 0,
  total: 0
});

const checkForUpdates = async () => {
  updateInfo.checking = true;
  try {
    const info = await CheckForUpdates();
    Object.assign(updateInfo, info);
    updateInfo.checking = false;
    
    if (info.available) {
      showModal('发现新版本', `当前版本: ${info.currentVersion}\n最新版本: ${info.latestVersion}\n\n点击"关于"面板中的"立即更新"按钮进行更新。`, 'info');
    } else {
      showModal('已是最新版本', `当前版本: ${info.currentVersion}`, 'success');
    }
  } catch (error) {
    updateInfo.checking = false;
    showModal('检查更新失败', String(error), 'error');
  }
};

const downloadAndInstall = async () => {
  if (!updateInfo.downloadUrl) return;
  
  updateProgress.downloading = true;
  updateProgress.progress = 0;
  
  try {
    await DownloadAndInstallUpdate(updateInfo.downloadUrl);
    showModal('更新成功', '应用将自动重启以使用新版本...', 'success');
    updateProgress.downloading = false;
  } catch (error) {
    updateProgress.downloading = false;
    showModal('更新失败', String(error), 'error');
  }
};



// --- 5. 性能优化：批量处理 ---
const incomingDataQueue: number[][] = [];
let animationFrameId: number | null = null;
let lastProcessTime = 0;
const PROCESS_INTERVAL = 30; // ms (approx 30fps)

// 把队列里的数据一次性处理完，返回处理的字节数。
// rAF 循环和 task-tx 之前的 drain 共用这一个函数：TX 行因此一定排在"它之前收到的" RX 之后，
// 排序只有这一个来源，不需要第二条 TX 队列。
const processIncomingBatch = (): number => {
    if (incomingDataQueue.length === 0) return 0;

    // 取出所有待处理数据
    const batchChunks = incomingDataQueue.splice(0, incomingDataQueue.length);

    // 计算总长度
    let totalLen = 0;
    for (const chunk of batchChunks) totalLen += chunk.length;
    if (totalLen === 0) return 0;

    // 合并为一个大数组
    const consolidatedBytes = new Array(totalLen);
    let offset = 0;
    for (const chunk of batchChunks) {
        for (let i = 0; i < chunk.length; i++) {
            consolidatedBytes[offset++] = chunk[i];
        }
    }

    try {
        // 1. 绘图解析
        parseAndPlot(consolidatedBytes);

        // 2. 更新 rawDataBuffer
        rawDataBuffer.value.push(...consolidatedBytes);
        if (rawDataBuffer.value.length > MAX_BUFFER_SIZE) {
             const overflow = rawDataBuffer.value.length - MAX_BUFFER_SIZE;
             rawDataBuffer.value.splice(0, overflow);
        }

        // 3. 更新 receivedData (显示文本) 与行视图
        // 行视图单独解码（自己的解码器实例，不推进 receivedData 的流式状态）；两种视图下都维护，
        // 否则 Hex 期间收到的内容在切回文本时会凭空消失
        const transcriptText = lineDecoder.decode(new Uint8Array(consolidatedBytes), { stream: true });
        if (showHex.value) {
             receivedData.value += formatData(consolidatedBytes, true);
        } else {
             // 文本模式按终端语义累加：退格删字符、CR/LF 归一、其余控制字符丢弃
             receivedData.value = applyTerminalChars(receivedData.value, formatData(consolidatedBytes, false));
        }
        pushRxChunk(transcriptText);

        // 截断
        if (receivedData.value.length > MAX_BUFFER_SIZE) {
            receivedData.value = receivedData.value.slice(receivedData.value.length - MAX_BUFFER_SIZE);
        }

        // 4. 更新计数
        rxCount.value += totalLen;
        return totalLen;
    } catch (e) {
        console.error("Error in processing loop:", e);
        return 0;
    }
};

const startProcessingLoop = () => {
    if (animationFrameId !== null) return;
    
    const loop = (timestamp: number) => {
        animationFrameId = requestAnimationFrame(loop);
        
        // 限流，避免过于频繁
        if (timestamp - lastProcessTime < PROCESS_INTERVAL) return;
        lastProcessTime = timestamp;
        
        if (incomingDataQueue.length === 0) return;
        
        if (processIncomingBatch() > 0) scrollToBottom();
    };
    
    animationFrameId = requestAnimationFrame(loop);
};

const stopProcessingLoop = () => {
    if (animationFrameId !== null) {
        cancelAnimationFrame(animationFrameId);
        animationFrameId = null;
    }
};

// --- 4. 生命周期 ---
onMounted(async () => {
  // 获取当前版本
  appVersion.value = await GetVersion();
  await refreshPorts();
  // TASK 页的设备列表：读取 exe 同目录的 pwr_dev.ini（不存在时自动创建）
  await loadPwrDevices();
  // TASK 页的任务参数：读取 task_config.ini（不存在时自动创建）
  await loadTaskConfig();
  // 右侧栏 AUTO CMD：读取 auto_cmd.txt（不存在时自动创建）
  await loadAutoCmds();

  // 数据接收监听
  EventsOn("serial-data", (data: any) => {
    let bytes: number[] = [];
    if (typeof data === 'string') {
      try {
        bytes = base64ToBytes(data);
      } catch (e) {
        console.error("Base64 decode error:", e);
        return;
      }
    } else if (Array.isArray(data)) {
      bytes = data;
    }

      // --- 修复开始：限制内存增长 ---
      // 批量处理优化：不再每次都在这里直接操作 heavy 的响应式对象
      // 而是推入到一个非响应式的队列中，由 requestAnimationFrame 循环处理
      incomingDataQueue.push(bytes);
  });

  // 启动处理循环
  startProcessingLoop();

  // 接收区获焦时的粘贴兜底（见 onDocumentPaste）
  document.addEventListener('paste', onDocumentPaste);

  // 任务发出的 TX 命令：另起一行显示（后端给的是可读形态——功能键保留 {UP} / {ENTER} 标记、
  // 不带行尾 CR/LF，time 是发送时刻 HH:MM:SS）
  EventsOn("task-tx", (data: any) => {
    if (!data) return;
    // 先把还压在队列里的 RX 处理掉再写这一行：rAF 循环 30ms 才跑一次，
    // 不 drain 的话命令会显示在它之前到达的回复上面（GET TEMP / TEMP=25.1 的顺序就反了）
    processIncomingBatch();
    pushTxLine(String(data.text ?? ''), String(data.time ?? ''));
  });

  EventsOn("serial-error", (err) => {
    console.error("Connection error:", err);
    isConnected.value = false;
    showModal("连接断开", String(err), 'error');
  });

  EventsOn("sys-msg", (msg) => {
    console.log("Sys Msg:", msg);
  });

  EventsOn("update-progress", (data: any) => {
    updateProgress.downloaded = data.downloaded;
    updateProgress.total = data.total;
    updateProgress.progress = data.progress;
  });
});

// 清理定时器防止内存泄漏
onUnmounted(() => {
  if (broomAnimationTimer) {
    clearTimeout(broomAnimationTimer);
    broomAnimationTimer = null;
  }
  document.removeEventListener('paste', onDocumentPaste);
  stopProcessingLoop();
  stopTaskPolling();
});


const base64ToBytes = (base64: string): number[] => {
  const binaryString = window.atob(base64);
  const len = binaryString.length;
  const bytes = new Array(len);
  for (let i = 0; i < len; i++) bytes[i] = binaryString.charCodeAt(i);
  return bytes;
};

const refreshPorts = async () => {
  try {
    portList.value = await GetSerialPorts();
    if (portList.value.length > 0 && !selectedPort.value) selectedPort.value = portList.value[0];
  } catch (e) { console.error(e); }
};

const toggleConnection = async () => {
  if (isConnected.value) {
    await CloseConnection();
    isConnected.value = false;
    return;
  }

  // TASK 页只做任务配置，不建立连接
  if (mode.value === 'TASK') return;

  let res = "";

  if (mode.value === 'SERIAL') {
    if (!selectedPort.value) return;
    res = await OpenSerial(selectedPort.value, Number(baudRate.value), Number(dataBits.value), Number(stopBits.value), parity.value);
  } else if (mode.value === 'NETWORK') {
    if (netMode.value === 'TCP_CLIENT') {
      if (!netIp.value || !netPort.value) return;
      res = await OpenTcpClient(netIp.value, netPort.value);
    } else if (netMode.value === 'TCP_SERVER') {
      if (!netPort.value) return;
      res = await OpenTcpServer(netPort.value);
    } else {
      if (!udpLocalPort.value) return;
      res = await OpenUdp(udpLocalPort.value, netIp.value, netPort.value);
    }
  }

  if (res === "Success") {
    isConnected.value = true;
  } else {
    showModal("连接失败", res, 'error');
  }
};

// 把输入框里的文本按当前 Hex / 行尾设置转换成真正要发送的字节串
// 返回 null 表示校验没通过（已经弹过提示）
const buildPayload = (text: string): string | null => {
  if (!text) return null;

  if (hexSend.value) {
    const cleanInput = text.replace(/\s+/g, '');
    if (!/^[0-9A-Fa-f]*$/.test(cleanInput)) {
      showModal("格式错误", "Hex 字符串包含非法字符 (0-9, A-F)", 'error');
      return null;
    }
    if (cleanInput.length % 2 !== 0) {
      showModal("格式错误", "Hex 字符串长度必须为偶数 (例如: AA BB)", 'error');
      return null;
    }
    let payload = "";
    for (let i = 0; i < cleanInput.length; i += 2) {
      payload += String.fromCharCode(parseInt(cleanInput.substring(i, i + 2), 16));
    }
    return payload;
  }

  let payload = text;
  if (lineEndingMode.value === 'LF') {
    payload += "\n";
  } else if (lineEndingMode.value === 'CRLF') {
    payload += "\r\n";
  }
  return payload;
};

// 统一的发送入口：TX EDITOR 回车发送走这里（按 Hex / 行尾设置转换）
const sendPayload = async (text: string): Promise<boolean> => {
  const payload = buildPayload(text);
  if (payload === null) return false;

  const res = await SendData(payload);
  if (res === 'Sent') {
    txCount.value += payload.length;
    return true;
  }

  showModal("发送失败", res, 'error');
  return false;
};

// 终端式直接发送：不经 Hex / 行尾转换，按原样发出（RX MONITOR 里键入即用）
const sendRaw = async (text: string): Promise<boolean> => {
  if (!text) return false;
  if (!isConnected.value) {
    showModal("未连接", "请先连接串口或网络，再发送数据", 'error');
    return false;
  }

  const res = await SendData(text);
  if (res === 'Sent') {
    txCount.value += text.length;
    return true;
  }

  showModal("发送失败", res, 'error');
  return false;
};

// ===== RX MONITOR：点击接收区后，键入的内容直接发送（MobaXterm 风格）=====
// 回车只发一个换行符：\r\n 会被很多下位机（如 letter-shell 同时接受 CR 和 LF）当成两次回车，
// 表现就是按一次回车却显示两行。所以 CRLF 设置在这里也退化成单个 \r。
const rxEnterBytes = (): string => (lineEndingMode.value === 'LF' ? '\n' : '\r');

// ↑ / ↓ 翻历史：先把当前行长按退格擦掉，再把历史内容重新敲一遍（终端式链路上唯一可行的做法）
const recallRxHistory = async (step: -1 | 1) => {
  if (rxHistory.value.length === 0) return;

  if (rxHistoryIndex.value === -1) {
    if (step === 1) return; // 还没翻历史时按 ↓ 什么也不做
    rxHistoryIndex.value = rxHistory.value.length - 1;
  } else {
    const next = rxHistoryIndex.value + step;
    if (next < 0) return;
    if (next >= rxHistory.value.length) {
      rxHistoryIndex.value = -1;
      const erased = '\x7f'.repeat(rxLineBuffer.value.length);
      rxLineBuffer.value = '';
      if (erased) await sendRaw(erased);
      return;
    }
    rxHistoryIndex.value = next;
  }

  const target = rxHistory.value[rxHistoryIndex.value] ?? '';
  const erase = '\x7f'.repeat(rxLineBuffer.value.length);
  if (erase) await sendRaw(erase);
  if (target) await sendRaw(target);
  rxLineBuffer.value = target;
};

const onRxKeydown = async (e: KeyboardEvent) => {
  if (e.isComposing) return;

  // Ctrl+C：有选中文本时是“复制”，交给浏览器；没有选中才当作打断发送 0x03
  if (e.ctrlKey && !e.altKey && !e.metaKey && (e.key === 'c' || e.key === 'C')) {
    // 行视图是普通 div，没有 selectionStart；textarea（Hex 视图）里选中的文字也不进
    // window.getSelection()，所以两种元素分开判断
    const el = e.target as HTMLElement | null;
    const hasSelection = el instanceof HTMLTextAreaElement
      ? el.selectionStart !== el.selectionEnd
      : !!window.getSelection()?.toString();
    if (hasSelection) return;
    e.preventDefault();
    rxLineBuffer.value = '';
    await sendRaw('\x03');
    return;
  }
  // Ctrl+字母：终端里对应的就是 0x01–0x1A（Ctrl+D 断开、Ctrl+Z 挂起、Ctrl+L 清屏…）。
  // A / C / V / X 留给浏览器（全选、复制、粘贴、剪切），接收区里这些操作比发控制码更有用。
  if (e.ctrlKey && !e.altKey && !e.metaKey && !e.shiftKey && e.key.length === 1) {
    const ch = e.key.toLowerCase();
    if (ch >= 'a' && ch <= 'z' && ch !== 'a' && ch !== 'c' && ch !== 'v' && ch !== 'x') {
      e.preventDefault();
      await sendRaw(String.fromCharCode(ch.charCodeAt(0) - 96));
      return;
    }
  }
  // 其余组合键留给浏览器（复制、全选等）
  if (e.ctrlKey || e.metaKey || e.altKey) return;

  if (e.key === 'Enter') {
    e.preventDefault();
    const line = rxLineBuffer.value;
    if (line) {
      if (rxHistory.value[rxHistory.value.length - 1] !== line) {
        rxHistory.value.push(line);
        if (rxHistory.value.length > 50) rxHistory.value.shift();
      }
    }
    rxHistoryIndex.value = -1;
    rxLineBuffer.value = '';
    await sendRaw(rxEnterBytes());
    nextTick(() => scrollToBottom());
    return;
  }
  if (e.key === 'ArrowUp') {
    e.preventDefault();
    await recallRxHistory(-1);
    return;
  }
  if (e.key === 'ArrowDown') {
    e.preventDefault();
    await recallRxHistory(1);
    return;
  }
  if (e.key === 'Backspace') {
    e.preventDefault();
    rxLineBuffer.value = rxLineBuffer.value.slice(0, -1);
    await sendRaw('\x7f');
    return;
  }
  // 剩下这些没有本地语义，按 VT100/xterm 的转义序列原样发给下位机
  const escKey = ESC_KEYS[e.key];
  if (escKey) {
    e.preventDefault();
    if (e.key === 'Escape') rxLineBuffer.value = '';
    await sendRaw(escKey);
    return;
  }
  if (e.key === 'Tab') {
    e.preventDefault();
    rxLineBuffer.value += '\t';
    await sendRaw('\t');
    return;
  }
  // 可打印字符：立刻发出
  if (e.key.length === 1) {
    e.preventDefault();
    rxLineBuffer.value += e.key;
    await sendRaw(e.key);
  }
};

// 粘贴：接收区是只读的，粘贴进来的内容直接发给下位机（终端式粘贴）
const onRxPaste = async (e: ClipboardEvent) => {
  e.preventDefault();
  const raw = e.clipboardData?.getData('text') ?? '';
  if (!raw) return;
  const payload = raw.replace(/\r\n/g, '\r').replace(/\n/g, '\r');
  rxLineBuffer.value += payload.replace(/\r/g, '');
  await sendRaw(payload);
};

// 行视图是普通 div，@paste 不一定收得到（不同内核/版本对非可编辑元素的路由不一样），
// 所以再挂一层 document 监听兜底：只要焦点还在接收区里就按同样的方式粘贴即发送。
// 元素自己的 @paste 已经 preventDefault 过，这里靠 defaultPrevented 去重，不会发两遍。
const onDocumentPaste = (e: ClipboardEvent) => {
  if (e.defaultPrevented) return;
  const box = logWindowRef.value;
  if (!box || !box.contains(document.activeElement)) return;
  void onRxPaste(e);
};

// ===== TX EDITOR：编辑后回车发送，↑ / ↓ 翻历史 =====
const handleSend = async () => {
  const text = sendInput.value;
  if (!text) return;

  const ok = await sendPayload(text);
  if (!ok) return;

  if (txHistory.value[txHistory.value.length - 1] !== text) {
    txHistory.value.push(text);
    if (txHistory.value.length > 50) txHistory.value.shift();
  }
  txHistoryIndex.value = -1;
  sendInput.value = '';
  nextTick(() => scrollToBottom());
};

const txHistoryPrev = () => {
  if (txHistory.value.length === 0) return;
  if (txHistoryIndex.value === -1) {
    txHistoryIndex.value = txHistory.value.length - 1;
  } else if (txHistoryIndex.value > 0) {
    txHistoryIndex.value -= 1;
  }
  sendInput.value = txHistory.value[txHistoryIndex.value] ?? '';
};

const txHistoryNext = () => {
  if (txHistory.value.length === 0 || txHistoryIndex.value === -1) return;
  if (txHistoryIndex.value < txHistory.value.length - 1) {
    txHistoryIndex.value += 1;
    sendInput.value = txHistory.value[txHistoryIndex.value] ?? '';
  } else {
    txHistoryIndex.value = -1;
    sendInput.value = '';
  }
};

// 清空接收数据时的动画状态
const BROOM_ANIMATION_DURATION = 600; // ms, 与 CSS 动画时长保持一致
const isBroomClicked = ref(false);
let broomAnimationTimer: ReturnType<typeof setTimeout> | null = null;

const clearReceive = () => {
  receivedData.value = '';
  rawDataBuffer.value = [];
  rxCount.value = 0;
  // 行视图也要一起清，否则清空后旧行还挂在屏幕上
  transcript.value = [];
  rxLineChars = [];
  rxLineCursor = 0;
  rxOpenIdx = -1;
  // 清除之前的定时器（如果存在）
  if (broomAnimationTimer) {
    clearTimeout(broomAnimationTimer);
  }
  
  // 触发清扫动画
  isBroomClicked.value = true;
  broomAnimationTimer = setTimeout(() => {
    isBroomClicked.value = false;
    broomAnimationTimer = null;
  }, BROOM_ANIMATION_DURATION);
};

const decoder = new TextDecoder('utf-8');
const formatData = (bytes: number[], isHex: boolean): string => {
  if (isHex) {
    return bytes.map(b => b.toString(16).padStart(2, '0').toUpperCase()).join(' ') + ' ';
  } else {
    return decoder.decode(new Uint8Array(bytes), { stream: true });
  }
};

watch(showHex, () => {
  receivedData.value = formatData(rawDataBuffer.value, showHex.value);
});

// 旧调用点（终端键入 / TX 发送后）沿用同一套滚到底逻辑，行视图和 Hex 视图都适用
const scrollToBottom = () => scrollLogToEnd();
</script>

<template>
  <div :style="cssVars as any" :data-skin="activeSkin" class="app-root flex h-screen w-screen bg-[var(--bg-main)] text-[var(--text-main)] font-sans overflow-hidden select-none">

    <!-- 侧边栏 -->
    <aside class="rail rail--l">
      <header class="rail-head">
        <div class="brand">
          <span class="brand-mark" aria-hidden="true">
            <svg width="13" height="13" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2.4" stroke-linecap="round" stroke-linejoin="round"><path d="M3 12h4l2.2-6 4.2 12L15.6 12H21"></path></svg>
          </span>
          <span class="brand-name">SerialMateX</span>
        </div>
        <div class="rail-actions">
          <button @click="showAboutPanel = !showAboutPanel" class="icon-btn" :class="{ 'is-on': showAboutPanel }" title="关于与更新">
            <svg xmlns="http://www.w3.org/2000/svg" width="16" height="16" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round"><circle cx="12" cy="12" r="10"></circle><line x1="12" y1="16" x2="12" y2="12"></line><line x1="12" y1="8" x2="12.01" y2="8"></line></svg>
          </button>
          <button @click="showSkinPanel = !showSkinPanel" class="icon-btn" :class="{ 'is-on': showSkinPanel }" :title="'一键换肤 · 当前：' + currentSkin.name">
            <svg xmlns="http://www.w3.org/2000/svg" width="16" height="16" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round"><path d="m12.83 2.18a2 2 0 0 0-1.66 0L2.6 6.08a1 1 0 0 0 0 1.83l8.58 3.91a2 2 0 0 0 1.66 0l8.58-3.9a1 1 0 0 0 0-1.83Z"></path><path d="m22 17.65-9.17 4.16a2 2 0 0 1-1.66 0L2 17.65"></path><path d="m22 12.65-9.17 4.16a2 2 0 0 1-1.66 0L2 12.65"></path></svg>
          </button>
        </div>
      </header>

      <!-- 换肤面板背景遮罩 -->
      <Transition name="backdrop-fade">
        <div v-if="showSkinPanel" @click="showSkinPanel = false" class="backdrop"></div>
      </Transition>

      <!-- 换肤面板 -->
      <Transition name="slide-down">
        <div v-if="showSkinPanel" @click.stop class="popover popover--skins">
          <div class="pop-head">
            <div class="pop-mark">
              <svg xmlns="http://www.w3.org/2000/svg" width="15" height="15" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round"><path d="m12.83 2.18a2 2 0 0 0-1.66 0L2.6 6.08a1 1 0 0 0 0 1.83l8.58 3.91a2 2 0 0 0 1.66 0l8.58-3.9a1 1 0 0 0 0-1.83Z"></path><path d="m22 17.65-9.17 4.16a2 2 0 0 1-1.66 0L2 17.65"></path><path d="m22 12.65-9.17 4.16a2 2 0 0 1-1.66 0L2 12.65"></path></svg>
            </div>
            <div class="min-w-0">
              <div class="pop-title">界面皮肤</div>
              <div class="pop-sub">点一下立即换肤，自动保存</div>
            </div>
          </div>

          <div class="skin-list">
            <button v-for="skin in SKINS" :key="skin.id" @click="applySkin(skin.id)"
              class="skin-item" :class="{ 'is-on': skin.id === activeSkin }">
              <span class="swatches" aria-hidden="true">
                <span v-for="(c, i) in skin.swatch" :key="i" class="swatch" :style="{ background: c }"></span>
              </span>
              <span class="skin-text">
                <span class="skin-name">{{ skin.name }}</span>
                <span class="skin-desc">{{ skin.desc }}</span>
              </span>
              <svg v-if="skin.id === activeSkin" class="skin-tick" xmlns="http://www.w3.org/2000/svg" width="13" height="13" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="3" stroke-linecap="round" stroke-linejoin="round"><polyline points="20 6 9 17 4 12"></polyline></svg>
            </button>
          </div>
        </div>
      </Transition>

      <!-- 关于面板背景遮罩 -->
      <Transition name="backdrop-fade">
        <div v-if="showAboutPanel" @click="showAboutPanel = false" class="backdrop"></div>
      </Transition>

      <!-- 关于与更新面板 -->
      <Transition name="slide-down">
        <div v-if="showAboutPanel" @click.stop class="popover popover--about">
          <div class="pop-head">
            <div class="pop-mark">
              <svg xmlns="http://www.w3.org/2000/svg" width="15" height="15" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2.2" stroke-linecap="round" stroke-linejoin="round"><circle cx="12" cy="12" r="10"></circle><line x1="12" y1="16" x2="12" y2="12"></line><line x1="12" y1="8" x2="12.01" y2="8"></line></svg>
            </div>
            <div class="min-w-0">
              <div class="pop-title">关于 SerialMateX</div>
              <div class="pop-sub">多功能串口通信工具</div>
            </div>
          </div>

          <div class="version-card">
            <div class="version-row">
              <span class="eyebrow">当前版本</span>
              <span class="version-pill">{{ appVersion }}</span>
            </div>
            <button @click="checkForUpdates" :disabled="updateInfo.checking" class="btn btn-block" :class="updateInfo.checking ? '' : 'btn-primary'">
              <svg v-if="!updateInfo.checking" xmlns="http://www.w3.org/2000/svg" width="13" height="13" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round"><path d="M21.5 2v6h-6M2.5 22v-6h6M2 11.5a10 10 0 0 1 18.8-4.3M22 12.5a10 10 0 0 1-18.8 4.2"></path></svg>
              <span v-else class="spinner"></span>
              <span>{{ updateInfo.checking ? '检查中...' : '检查更新' }}</span>
            </button>
          </div>

          <div v-if="updateInfo.available" class="update-card">
            <div class="update-head">
              <span class="badge-new">NEW</span>
              <span class="update-title">发现新版本</span>
            </div>
            <div class="update-versions">
              <span class="mono">{{ updateInfo.currentVersion }}</span>
              <svg xmlns="http://www.w3.org/2000/svg" width="12" height="12" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round" class="opacity-50"><polyline points="9 18 15 12 9 6"></polyline></svg>
              <span class="mono update-next">{{ updateInfo.latestVersion }}</span>
            </div>
            <div v-if="updateInfo.releaseNotes" class="notes">
              <div class="micro">更新内容</div>
              <div class="notes-body">{{ updateInfo.releaseNotes }}</div>
            </div>
            <button @click="downloadAndInstall" :disabled="updateProgress.downloading" class="btn btn-primary btn-block">
              <svg v-if="!updateProgress.downloading" xmlns="http://www.w3.org/2000/svg" width="13" height="13" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round"><path d="M21 15v4a2 2 0 0 1-2 2H5a2 2 0 0 1-2-2v-4"></path><polyline points="7 10 12 15 17 10"></polyline><line x1="12" y1="15" x2="12" y2="3"></line></svg>
              <span v-else class="spinner"></span>
              <span>{{ updateProgress.downloading ? '下载中...' : '立即更新' }}</span>
            </button>
            <div v-if="updateProgress.downloading" class="progress-row">
              <div class="progress-meta">
                <span class="mono">{{ (updateProgress.downloaded / 1024 / 1024).toFixed(2) }} MB / {{ (updateProgress.total / 1024 / 1024).toFixed(2) }} MB</span>
                <span class="mono">{{ updateProgress.progress.toFixed(0) }}%</span>
              </div>
              <div class="progress-track"><div class="progress-fill" :style="{ width: updateProgress.progress + '%' }"></div></div>
            </div>
          </div>
        </div>
      </Transition>

      <div class="rail-body custom-scrollbar">
        <!-- 功能标签页：SERIAL 与 TASK 并排；NETWORK 收进右侧独立的“三横杠”按钮弹出菜单 -->
        <div class="mode-row" :class="{ 'shake-anim': isShaking }">
          <div class="seg">
            <button v-for="tab in modeTabs" :key="tab.key" @click="switchMode(tab.key)" :class="{ 'is-on': mode === tab.key }">
              {{ tab.label }}
            </button>
          </div>

          <div class="relative shrink-0">
            <button @click="showMoreMenu = !showMoreMenu" class="icon-btn icon-btn--square" :class="{ 'is-on': showMoreMenu || mode === 'NETWORK' }" title="更多功能：NETWORK">
              <svg class="w-4 h-4 overflow-visible" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round">
                <line x1="3" y1="6" x2="21" y2="6" class="transition-all duration-300 origin-[12px_12px]" :class="showMoreMenu ? 'translate-y-[6px] rotate-45' : ''"></line>
                <line x1="3" y1="12" x2="21" y2="12" class="transition-all duration-300" :class="showMoreMenu ? 'opacity-0' : ''"></line>
                <line x1="3" y1="18" x2="21" y2="18" class="transition-all duration-300 origin-[12px_12px]" :class="showMoreMenu ? '-translate-y-[6px] -rotate-45' : ''"></line>
              </svg>
            </button>

            <div v-if="showMoreMenu" @click="showMoreMenu = false" class="fixed inset-0 z-40 cursor-default"></div>
            <Transition name="dropdown-fade">
              <div v-if="showMoreMenu" class="menu menu--more">
                <button @click="switchMode('NETWORK')" class="menu-item" :class="{ 'is-on': mode === 'NETWORK' }">
                  <span>NETWORK</span>
                  <span v-if="mode === 'NETWORK'" class="menu-tick">✓</span>
                </button>
              </div>
            </Transition>
          </div>
        </div>

        <!-- 设置面板主体（任务运行期间只读，避免边跑边改参数） -->
        <section class="panel settings-card" :class="{ 'is-locked': taskLocked }">
          <div class="panel-head">
            <span class="eyebrow">{{ mode === 'TASK' ? 'TASK' : mode }} settings</span>
            <span class="conn-dot" :class="{ 'is-on': isConnected }" :title="isConnected ? '已连接' : '未连接'"></span>
          </div>

          <div class="panel-body">
            <div v-if="mode === 'SERIAL'" key="SERIAL" class="field-stack">
              <!-- 端口 Port -->
              <div class="field-row">
                <label class="field-label">端口</label>
                <div class="field-wrap">
                  <button @click="!isConnected && (refreshPorts(), showPortDropdown = !showPortDropdown)" class="field field--btn" :class="{ 'is-disabled': isConnected }">
                    <span class="truncate">{{ selectedPort || '选择端口' }}</span>
                    <svg class="chev" :class="{ 'is-open': showPortDropdown }" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round"><polyline points="6 9 12 15 18 9"></polyline></svg>
                  </button>
                  <div v-if="showPortDropdown && !isConnected" @click="showPortDropdown = false" class="fixed inset-0 z-0 cursor-default"></div>
                  <Transition name="slide-fade">
                    <div v-if="showPortDropdown && !isConnected" class="menu">
                      <button v-for="p in portList" :key="p" @click="selectedPort = p; showPortDropdown = false" class="menu-item" :class="{ 'is-on': selectedPort === p }">
                        <span>{{ p }}</span>
                        <span v-if="selectedPort === p" class="menu-tick">✓</span>
                      </button>
                      <div v-if="portList.length === 0" class="menu-empty">无可用端口</div>
                    </div>
                  </Transition>
                </div>
              </div>

              <!-- 波特率 Baud Rate：典型值下拉，与数据位/停止位同款控件 -->
              <div class="field-row">
                <label class="field-label">波特率</label>
                <div class="field-wrap">
                  <button @click="!isConnected && (showBaudDropdown = !showBaudDropdown)" class="field field--btn" :class="{ 'is-disabled': isConnected }">
                    <span class="mono">{{ baudRate }}</span>
                    <svg class="chev" :class="{ 'is-open': showBaudDropdown }" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round"><polyline points="6 9 12 15 18 9"></polyline></svg>
                  </button>
                  <div v-if="showBaudDropdown && !isConnected" @click="showBaudDropdown = false" class="fixed inset-0 z-0 cursor-default"></div>
                  <Transition name="slide-fade">
                    <div v-if="showBaudDropdown && !isConnected" class="menu">
                      <button v-for="b in baudMenuOptions" :key="b" @click="baudRate = b; showBaudDropdown = false" class="menu-item" :class="{ 'is-on': baudRate === b }">
                        <span class="mono">{{ b }}</span>
                        <span v-if="baudRate === b" class="menu-tick">✓</span>
                      </button>
                    </div>
                  </Transition>
                </div>
              </div>

              <!-- 数据位 Data Bits -->
              <div class="field-row">
                <label class="field-label">数据位</label>
                <div class="field-wrap">
                  <button @click="!isConnected && (showDataBitsDropdown = !showDataBitsDropdown)" class="field field--btn" :class="{ 'is-disabled': isConnected }">
                    <span>{{ dataBits }}</span>
                    <svg class="chev" :class="{ 'is-open': showDataBitsDropdown }" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round"><polyline points="6 9 12 15 18 9"></polyline></svg>
                  </button>
                  <div v-if="showDataBitsDropdown && !isConnected" @click="showDataBitsDropdown = false" class="fixed inset-0 z-0 cursor-default"></div>
                  <Transition name="slide-fade">
                    <div v-if="showDataBitsDropdown && !isConnected" class="menu">
                      <button v-for="opt in dataBitsOptions" :key="opt" @click="dataBits = opt; showDataBitsDropdown = false" class="menu-item" :class="{ 'is-on': dataBits === opt }">
                        <span>{{ opt }}</span>
                        <span v-if="dataBits === opt" class="menu-tick">✓</span>
                      </button>
                    </div>
                  </Transition>
                </div>
              </div>

              <!-- 停止位 Stop Bits -->
              <div class="field-row">
                <label class="field-label">停止位</label>
                <div class="field-wrap">
                  <button @click="!isConnected && (showStopBitsDropdown = !showStopBitsDropdown)" class="field field--btn" :class="{ 'is-disabled': isConnected }">
                    <span>{{ stopBits }}</span>
                    <svg class="chev" :class="{ 'is-open': showStopBitsDropdown }" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round"><polyline points="6 9 12 15 18 9"></polyline></svg>
                  </button>
                  <div v-if="showStopBitsDropdown && !isConnected" @click="showStopBitsDropdown = false" class="fixed inset-0 z-0 cursor-default"></div>
                  <Transition name="slide-fade">
                    <div v-if="showStopBitsDropdown && !isConnected" class="menu">
                      <button v-for="opt in stopBitsOptions" :key="opt" @click="stopBits = opt; showStopBitsDropdown = false" class="menu-item" :class="{ 'is-on': stopBits === opt }">
                        <span>{{ opt }}</span>
                        <span v-if="stopBits === opt" class="menu-tick">✓</span>
                      </button>
                    </div>
                  </Transition>
                </div>
              </div>

              <!-- 校验位 Parity -->
              <div class="field-row">
                <label class="field-label">校验位</label>
                <div class="field-wrap">
                  <button @click="!isConnected && (showParityDropdown = !showParityDropdown)" class="field field--btn" :class="{ 'is-disabled': isConnected }">
                    <span>{{ parity }}</span>
                    <svg class="chev" :class="{ 'is-open': showParityDropdown }" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round"><polyline points="6 9 12 15 18 9"></polyline></svg>
                  </button>
                  <div v-if="showParityDropdown && !isConnected" @click="showParityDropdown = false" class="fixed inset-0 z-0 cursor-default"></div>
                  <Transition name="slide-fade">
                    <div v-if="showParityDropdown && !isConnected" class="menu">
                      <button v-for="opt in parityOptions" :key="opt.value" @click="parity = opt.value; showParityDropdown = false" class="menu-item" :class="{ 'is-on': parity === opt.value }">
                        <span>{{ opt.label }}</span>
                        <span v-if="parity === opt.value" class="menu-tick">✓</span>
                      </button>
                    </div>
                  </Transition>
                </div>
              </div>

              <!-- 显示选项：Hex 显示 / 自动滚屏 -->
              <div class="panel-sep">
                <div class="micro">Display</div>
                <label class="check-row">
                  <input type="checkbox" v-model="showHex" class="check">
                  <span>Hex 显示</span>
                </label>
                <label class="check-row">
                  <input type="checkbox" v-model="autoScroll" class="check">
                  <span>自动滚屏</span>
                </label>
              </div>
            </div>

            <!-- Network Settings -->
            <div v-else-if="mode === 'NETWORK'" key="NETWORK" class="field-stack">
              <div class="seg seg--sub">
                <button v-for="opt in netModeTabs" :key="opt.key" @click="switchNetMode(opt.key)" :class="{ 'is-on': netMode === opt.key }">
                  {{ opt.label }}
                </button>
              </div>

              <template v-if="netMode === 'TCP_CLIENT'">
                <div class="field-row"><label class="field-label">IP</label><div class="field-wrap"><input type="text" v-model="netIp" class="field mono" placeholder="127.0.0.1" :disabled="isConnected"></div></div>
                <div class="field-row"><label class="field-label">Port</label><div class="field-wrap"><input type="text" v-model="netPort" class="field mono" placeholder="43211" :disabled="isConnected"></div></div>
              </template>

              <template v-else-if="netMode === 'TCP_SERVER'">
                <div class="field-row"><label class="field-label">Local Port</label><div class="field-wrap"><input type="text" v-model="netPort" class="field mono" placeholder="8080" :disabled="isConnected"></div></div>
              </template>

              <template v-else>
                <div class="field-row"><label class="field-label">Local Port</label><div class="field-wrap"><input type="text" v-model="udpLocalPort" class="field mono" placeholder="8081" :disabled="isConnected"></div></div>
                <div class="panel-sep"></div>
                <div class="field-row"><label class="field-label">Target IP</label><div class="field-wrap"><input type="text" v-model="netIp" class="field mono" placeholder="127.0.0.1" :disabled="isConnected"></div></div>
                <div class="field-row"><label class="field-label">Target Port</label><div class="field-wrap"><input type="text" v-model="netPort" class="field mono" placeholder="8080" :disabled="isConnected"></div></div>
              </template>
            </div>

            <!-- TASK：设备选择（读取 exe 同目录的 pwr_dev.ini） -->
            <div v-else-if="mode === 'TASK'" key="TASK" class="field-stack">
              <div class="field-row">
                <label class="field-label">设备选择</label>
                <div class="field-wrap">
                  <button @click="!isConnected && (showDeviceDropdown = !showDeviceDropdown)" class="field field--btn" :class="{ 'is-disabled': isConnected }">
                    <span class="truncate">{{ selectedDeviceName || '未找到设备' }}</span>
                    <svg class="chev" :class="{ 'is-open': showDeviceDropdown }" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round"><polyline points="6 9 12 15 18 9"></polyline></svg>
                  </button>
                  <div v-if="showDeviceDropdown && !isConnected" @click="showDeviceDropdown = false" class="fixed inset-0 z-0 cursor-default"></div>
                  <Transition name="slide-fade">
                    <div v-if="showDeviceDropdown && !isConnected" class="menu">
                      <button v-for="dev in pwrDevices" :key="dev.name" @click="selectedDeviceName = dev.name; showDeviceDropdown = false" class="menu-item" :class="{ 'is-on': selectedDeviceName === dev.name }">
                        <span class="truncate">{{ dev.name }}</span>
                        <span v-if="selectedDeviceName === dev.name" class="menu-tick">✓</span>
                      </button>
                      <div v-if="pwrDevices.length === 0" class="menu-empty">pwr_dev.ini 中没有设备</div>
                    </div>
                  </Transition>
                </div>
              </div>

              <button @click="openDeviceEditor" class="btn btn-block btn-device">
                <svg class="w-3.5 h-3.5 opacity-70" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round"><path d="M12 20h9"></path><path d="M16.5 3.5a2.12 2.12 0 0 1 3 3L7 19l-4 1 1-4Z"></path></svg>
                编辑设备参数
              </button>

              <div class="panel-sep">
                <div class="setting-row">
                  <span class="setting-label">保存日志</span>
                  <button @click="toggleTaskFlag('saveLog')" class="switch" :class="{ 'is-on': taskConfig.saveLog }" :title="taskConfig.saveLog ? '点击改为关' : '点击改为开'"><span class="switch-knob"></span><span class="switch-text">{{ taskConfig.saveLog ? '开' : '关' }}</span></button>
                </div>
                <div class="setting-row">
                  <span class="setting-label">保存功耗</span>
                  <button disabled class="switch is-off" title="当前版本不支持：固定为关"><span class="switch-knob"></span><span class="switch-text">关</span></button>
                </div>
                <div class="setting-row">
                  <span class="setting-label">初始状态</span>
                  <button @click="toggleTaskFlag('initialState')" class="switch" :class="{ 'is-on': taskConfig.initialState }" :title="taskConfig.initialState ? '点击改为关' : '点击改为开'"><span class="switch-knob"></span><span class="switch-text">{{ taskConfig.initialState ? '开' : '关' }}</span></button>
                </div>
                <div class="setting-row">
                  <span class="setting-label">异常暂停</span>
                  <button @click="toggleTaskFlag('abnormalPause')" class="switch" :class="{ 'is-on': taskConfig.abnormalPause }" :title="taskConfig.abnormalPause ? '点击改为关' : '点击改为开'"><span class="switch-knob"></span><span class="switch-text">{{ taskConfig.abnormalPause ? '开' : '关' }}</span></button>
                </div>
              </div>

              <div class="panel-sep">
                <div class="setting-row">
                  <span class="setting-label">运行时间</span>
                  <button @click="openNumberDialog('waitSeconds')" class="chip chip--num" title="点击编辑运行时间">
                    <span class="tabular-nums">{{ taskConfig.waitSeconds }}</span><span class="chip-unit">秒</span>
                  </button>
                </div>
                <div class="setting-row">
                  <span class="setting-label">间隔时间</span>
                  <button @click="openNumberDialog('delaySeconds')" class="chip chip--num" title="点击编辑间隔时间">
                    <span class="tabular-nums">{{ taskConfig.delaySeconds }}</span><span class="chip-unit">秒</span>
                  </button>
                </div>
                <div class="setting-row">
                  <span class="setting-label">运行次数</span>
                  <button @click="openNumberDialog('runCount')" class="chip chip--num" title="点击编辑运行次数">
                    <span class="tabular-nums">{{ taskConfig.runCount }}</span><span class="chip-unit">次</span>
                  </button>
                </div>
              </div>
            </div>
          </div>
        </section>

        <!-- TASK STATUS：只在 TASK 页显示 -->
        <section v-if="mode === 'TASK'" class="panel">
          <div class="panel-head">
            <span class="eyebrow">Task status</span>
            <span class="status-pill" :class="taskRt.paused ? 'is-warn' : (taskRt.running ? 'is-live' : '')">{{ taskRt.paused ? 'PAUSED' : (taskRt.running ? 'RUN' : 'IDLE') }}</span>
          </div>
          <div class="panel-body">
            <div class="stat-row">
              <span class="setting-label">当前轮次</span>
              <span class="stat-value">
                {{ taskStatus.round }}<span class="stat-sub">次</span>/<span class="stat-accent">{{ taskConfig.runCount }}</span><span class="stat-sub">次</span>
              </span>
            </div>
            <div class="stat-row">
              <span class="setting-label">正常次数</span>
              <span class="stat-value"><span class="stat-ok">{{ taskRt.normalCount }}</span><span class="stat-sub">次</span></span>
            </div>
            <div class="stat-row">
              <span class="setting-label">电源状态</span>
              <span class="chip" :class="taskStatus.power === 'ON' ? 'is-ok' : 'is-off'">{{ taskStatus.power }}</span>
            </div>
            <div class="stat-row">
              <span class="setting-label">下一动作</span>
              <span class="stat-value stat-accent">
                <template v-if="taskRt.running && taskRt.countdown > 0 && taskRt.countdownTo">
                  {{ taskRt.countdown }} 秒后{{ taskRt.countdownTo === 'powerOn' ? '上电' : '掉电' }}
                </template>
                <template v-else-if="taskRt.paused">已暂停</template>
                <template v-else>—</template>
              </span>
            </div>
            <div class="stat-row" title="当前版本不支持电流采集：固定显示 NULL">
              <span class="setting-label">当前电流</span>
              <span class="chip is-off">NULL<span class="chip-unit">A</span></span>
            </div>
            <div v-if="taskRt.paused" class="paused-note">
              <span class="paused-note__dot"></span>
              <span>{{ taskRt.message || '有命令未执行完，已暂停' }}</span>
            </div>
          </div>
        </section>
      </div>

      <!-- 连接：固定在左栏底部，始终可达 -->
      <div v-if="mode !== 'TASK'" class="rail-foot">
        <button @click="toggleConnection" :disabled="taskLocked" class="btn btn-primary btn-connect" :class="{ 'is-connected': isConnected, 'is-disabled': taskLocked }" :title="taskLocked ? '任务运行中，先停止任务再断开连接' : ''">
          <span v-if="!isConnected" class="dot dot--pulse"></span>
          <span>{{ isConnected ? '断开' : '连接' }}</span>
        </button>
      </div>
    </aside>

    <!-- 中间主区域 (RX / TX)；宽度最小为左侧栏的 2 倍（288 × 2 = 576px） -->
    <main class="stage">
      <section class="panel rx-panel">
        <header class="panel-bar">
          <div class="bar-group">
            <span class="eyebrow">RX monitor</span>
            <span class="count-pill mono">{{ rxCount }} Bytes</span>
            <span v-if="isConnected" class="bar-hint">点击接收区即可直接键入发送</span>
          </div>
          <div class="bar-group">
            <div class="seg seg--xs">
              <button @click="showChart = false" :class="{ 'is-on': !showChart }">Text</button>
              <button disabled title="暂未开放" :class="{ 'is-on': showChart }">Plot</button>
            </div>
            <button @click="clearReceive" title="清空" class="icon-btn clear-btn">
              <svg class="broom-icon" :class="{ 'broom-clicked': isBroomClicked }" width="15" height="15" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.8" stroke-linecap="round" stroke-linejoin="round"><path d="m7 21-4-4 10-10a2.83 2.83 0 0 1 4 4L7 21Z"></path><path d="m11 9 4 4"></path><path d="M3 21h18"></path></svg>
            </button>
          </div>
        </header>

        <div v-if="showChart" class="chart-wrap">
          <v-chart class="chart" :option="chartOption" autoresize />
        </div>
        <!-- 文本模式：行视图（RX 逐行、TX 绿色带时间戳）；Hex 模式回到字节 dump -->
        <div v-else-if="!showHex"
             ref="logWindowRef"
             class="rx-log custom-scrollbar"
             tabindex="0"
             role="textbox"
             aria-multiline="true"
             :aria-label="RX_HINT"
             @keydown="onRxKeydown"
             @paste="onRxPaste">
          <!-- 空态提示：等价于原来 textarea 的 placeholder，行视图是 div 用不了 ::placeholder -->
          <div v-if="!transcript.length" class="rx-empty">{{ RX_HINT }}</div>
          <div v-for="line in transcript" :key="line.id" class="rx-line" :class="{ 'is-tx': line.dir === 'tx' }"><span v-if="line.dir === 'tx'" class="rx-tag">{{ line.time }}</span>{{ line.text }}</div>
        </div>
        <textarea v-else
                  ref="logWindowRef"
                  readonly
                  class="rx-text custom-scrollbar"
                  :value="receivedData"
                  :placeholder="RX_HINT"
                  @keydown="onRxKeydown"
                  @paste="onRxPaste"></textarea>
      </section>

      <!-- TX EDITOR（可折叠；展开/收起按钮固定在整列最下方） -->
      <section class="tx" :class="showTxEditor ? 'is-open' : 'is-closed'">
        <div class="panel tx-card">
          <header class="tx-bar">
            <div class="bar-group">
              <span class="eyebrow">TX editor</span>
              <label class="check-row check-row--inline">
                <input type="checkbox" v-model="hexSend" class="check">
                <span>Hex Send</span>
              </label>
            </div>
            <div class="bar-group bar-group--end">
              <div class="relative" :class="{ 'is-muted': hexSend }">
                <button @click="showEolDropdown = !showEolDropdown" class="eol-btn" :class="{ 'is-on': showEolDropdown }">
                  <span class="eol-label">EOL</span>
                  <span class="eol-value mono">{{ currentEolLabel }}</span>
                  <svg class="chev" :class="{ 'is-open': showEolDropdown }" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round"><polyline points="6 9 12 15 18 9"></polyline></svg>
                </button>
                <div v-if="showEolDropdown" @click="showEolDropdown = false" class="fixed inset-0 z-0 cursor-default"></div>
                <Transition name="slide-fade">
                  <div v-if="showEolDropdown" class="menu menu--up">
                    <button v-for="opt in eolOptions" :key="opt.value" @click="selectEol(opt.value as any)" class="menu-item mono" :class="{ 'is-on': lineEndingMode === opt.value }">
                      <span>{{ opt.label }}</span>
                      <span v-if="lineEndingMode === opt.value" class="menu-tick">✓</span>
                    </button>
                  </div>
                </Transition>
              </div>
            </div>
          </header>

          <!-- 单行输入：编辑后回车发送，↑ / ↓ 翻历史 -->
          <div class="tx-input-row">
            <input ref="txInputRef"
                   v-model="sendInput"
                   type="text"
                   spellcheck="false"
                   autocomplete="off"
                   class="tx-input mono"
                   :placeholder="hexSend ? '输入 Hex（如 AA BB CC）后回车发送' : '输入内容后回车发送，↑ / ↓ 翻历史'"
                   @keydown.enter.prevent="handleSend"
                   @keydown.up.prevent="txHistoryPrev"
                   @keydown.down.prevent="txHistoryNext" />
          </div>
        </div>
      </section>

      <!-- 细长按钮：展开 / 收起 TX EDITOR -->
      <button @click="showTxEditor = !showTxEditor" class="strip" :title="showTxEditor ? '收起 TX EDITOR' : '展开 TX EDITOR'">
        <svg class="w-3 h-3 transition-transform duration-300" :class="{ 'rotate-180': !showTxEditor }" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="3" stroke-linecap="round" stroke-linejoin="round"><polyline points="6 15 12 9 18 15"></polyline></svg>
        <span>TX EDITOR</span>
      </button>
    </main>

    <!-- 右侧栏：AUTO CMD（宽度与左侧栏一致 288px，不随窗口缩放） -->
    <aside class="rail rail--r">
      <header class="cmd-head">
        <div class="cmd-head-title">
          <span class="eyebrow">Auto cmd</span>
          <span class="count-pill mono">{{ autoCmds.length }}</span>
        </div>
        <div class="cmd-actions">
          <button @click="addAutoCmd"
                  :disabled="taskLocked"
                  class="btn btn-xs btn-accent"
                  title="新增一条命令（有选中时插入到该条上方）">新增</button>
          <button @click="removeAutoCmd"
                  :disabled="autoCmdSelected < 0 || taskLocked"
                  class="btn btn-xs btn-ghost"
                  title="删除选中的命令">删除</button>
          <button @click="importAutoCmds"
                  :disabled="taskLocked"
                  class="btn btn-xs btn-ghost"
                  title="从 txt 文件导入命令（覆盖当前列表）">导入</button>
        </div>
      </header>

      <!-- 命令列表：序号 + 文本，单击选中，双击编辑，点空白处取消选中；运行期间只读且高亮当前命令 -->
      <div class="cmd-list custom-scrollbar"
           :class="{ 'is-locked': taskLocked }"
           @click.self="autoCmdSelected = -1">
        <button v-for="(cmd, i) in autoCmds" :key="i"
                @click="autoCmdSelected = i"
                @dblclick="openAutoCmdDialog(i)"
                :title="`${cmd.type} ${cmd.text}`"
                class="cmd-row"
                :class="[
                  taskRt.running && taskRt.cmdIndex === i
                    ? (taskRt.cmdPhase === 'wait' ? 'is-live is-wait' : 'is-live')
                    : '',
                  autoCmdSelected === i ? 'is-selected' : ''
                ]">
          <span class="cmd-num mono">{{ i + 1 }}</span>
          <!-- TX / RX 标志：TX 绿色、RX 红色，与序号、命令文本都能区分开 -->
          <span class="cmd-type" :class="cmd.type === 'RX' ? 'is-rx' : 'is-tx'">{{ cmd.type }}</span>
          <span class="cmd-text mono">{{ cmd.text }}</span>
          <!-- 行尾数字：固定两位（00 ~ 99）；运行到该条时用倒计时覆盖显示 -->
          <span class="cmd-count mono"
                :class="taskRt.running && taskRt.cmdIndex === i && taskRt.cmdPhase === 'wait' ? 'is-live' : ''"
                :title="taskRt.running && taskRt.cmdIndex === i ? '间隔倒计时' : '双击命令可修改（含延时）'">
            {{ taskRt.running && taskRt.cmdIndex === i && taskRt.cmdPhase === 'wait'
              ? String(taskRt.cmdCountdown)
              : formatCmdNumber(cmd.number) }}
          </span>
        </button>
        <div v-if="autoCmds.length === 0" @click="autoCmdSelected = -1" class="cmd-empty">
          <span>点击「新增」添加命令</span>
          <span class="opacity-60">双击可编辑</span>
        </div>
      </div>

      <!-- 底部：启动 / 停止（运行中沿边框均匀顺时针流光，停止时外圈呼吸灯）；异常暂停时改为两条出路 -->
      <div class="run-foot" :class="{ 'run-foot--paused': taskRt.paused }">
        <div class="run-ring" :class="taskRt.paused ? 'run-ring--paused' : (taskRt.running ? 'run-ring--running' : 'run-ring--idle')">
          <template v-if="taskRt.paused">
            <button @click="endTask" class="btn btn-warn" :disabled="taskBusy" title="结束任务：掉电并结束整个任务">结束任务</button>
            <button @click="skipRound" class="btn btn-primary" :disabled="taskBusy" title="跳过本轮：本轮流不计数，掉电后等间隔再跑下一轮">跳过本轮</button>
          </template>
          <template v-else>
            <!-- 流光用 SVG 描边 + pathLength 归一化：整圈按弧长匀速跑，长短边不会有快慢差 -->
            <svg v-if="taskRt.running" class="run-ring__svg" viewBox="0 0 100 100" preserveAspectRatio="none" aria-hidden="true">
              <defs>
                <linearGradient id="runRingGrad" x1="0" y1="0" x2="1" y2="1">
                  <stop offset="0%" stop-color="rgba(255,255,255,0.35)" />
                  <stop offset="45%" stop-color="var(--col-primary)" />
                  <stop offset="100%" stop-color="#ffffff" />
                </linearGradient>
              </defs>
              <!-- 底圈：让边框始终是“一圈环”，不至于在暗处看不见 -->
              <rect x="1.5" y="1.5" width="97" height="97" rx="6" ry="6" fill="none"
                    stroke="rgba(255,255,255,0.30)" stroke-width="2.5" vector-effect="non-scaling-stroke" />
              <!-- 高光段：沿整圈匀速流动 -->
              <rect x="1.5" y="1.5" width="97" height="97" rx="6" ry="6" fill="none"
                    stroke="url(#runRingGrad)" stroke-width="2.5" stroke-linecap="round"
                    pathLength="100" stroke-dasharray="20 80" vector-effect="non-scaling-stroke"
                    class="run-ring__dash" />
            </svg>
            <button @click="toggleAutoRun"
                    :disabled="!taskRt.running && !canStartTask"
                    class="run-btn"
                    :class="[
                      taskRt.running ? 'is-running' : '',
                      !taskRt.running && !canStartTask ? 'is-disabled' : ''
                    ]"
                    :title="!taskRt.running && !canStartTask ? '请先在 SERIAL 页连接 COM 口' : (taskRt.running ? '停止任务' : '开始任务')">
              {{ taskRt.running ? '停止' : '启动' }}
            </button>
          </template>
        </div>
      </div>
    </aside>

    <!-- TASK：设备参数编辑弹窗（新增 / 删除 / 保存 / 取消） -->
    <Transition name="modal-fade">
      <div v-if="showDeviceEditor" class="modal-backdrop">
        <div class="modal modal--devices" @click.stop>
          <div class="modal-head">
            <div class="modal-title">
              <svg class="modal-ico" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round"><path d="M12 20h9"></path><path d="M16.5 3.5a2.12 2.12 0 0 1 3 3L7 19l-4 1 1-4Z"></path></svg>
              <span>编辑设备参数</span>
            </div>
            <button @click="cancelDeviceEditor" class="modal-x" title="关闭"><svg viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round"><line x1="18" y1="6" x2="6" y2="18"></line><line x1="6" y1="6" x2="18" y2="18"></line></svg></button>
          </div>

          <div class="modal-body custom-scrollbar">
            <div v-for="(row, idx) in deviceDraft"
                 :key="idx"
                 @click="deviceDraftIndex = idx"
                 class="draft-row"
                 :class="{ 'is-on': deviceDraftIndex === idx }">
              <div class="draft-head">
                <span>设备 {{ idx + 1 }}</span>
                <span v-if="deviceDraftIndex === idx" class="draft-tick">当前选中</span>
              </div>
              <div class="draft-grid">
                <label class="draft-cell"><span class="draft-label">名称</span><input v-model="row.name" placeholder="设备名称" class="field" /></label>
                <label class="draft-cell"><span class="draft-label">端口</span><input v-model="row.port" placeholder="COM 端口，如 COM7" class="field mono" /></label>

                <div class="draft-cell">
                  <span class="draft-label">波特率</span>
                  <div class="field-wrap">
                    <button @click="toggleDeviceMenu(idx + ':baud')" class="field field--btn">
                      <span>{{ row.baud }}</span>
                      <svg class="chev" :class="{ 'is-open': deviceMenu === idx + ':baud' }" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round"><polyline points="6 9 12 15 18 9"></polyline></svg>
                    </button>
                    <div v-if="deviceMenu === idx + ':baud'" @click="deviceMenu = ''" class="fixed inset-0 z-0 cursor-default"></div>
                    <Transition name="slide-fade">
                      <div v-if="deviceMenu === idx + ':baud'" class="menu">
                        <button v-for="opt in deviceBaudOptions" :key="opt" @click="row.baud = opt; deviceMenu = ''" class="menu-item" :class="{ 'is-on': row.baud === opt }">
                          <span>{{ opt }}</span>
                          <span v-if="row.baud === opt" class="menu-tick">✓</span>
                        </button>
                      </div>
                    </Transition>
                  </div>
                </div>

                <div class="draft-cell">
                  <span class="draft-label">数据位</span>
                  <div class="field-wrap">
                    <button @click="toggleDeviceMenu(idx + ':dataBits')" class="field field--btn">
                      <span>{{ row.dataBits }}</span>
                      <svg class="chev" :class="{ 'is-open': deviceMenu === idx + ':dataBits' }" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round"><polyline points="6 9 12 15 18 9"></polyline></svg>
                    </button>
                    <div v-if="deviceMenu === idx + ':dataBits'" @click="deviceMenu = ''" class="fixed inset-0 z-0 cursor-default"></div>
                    <Transition name="slide-fade">
                      <div v-if="deviceMenu === idx + ':dataBits'" class="menu">
                        <button v-for="opt in dataBitsOptions" :key="opt" @click="row.dataBits = opt; deviceMenu = ''" class="menu-item" :class="{ 'is-on': row.dataBits === opt }">
                          <span>{{ opt }}</span>
                          <span v-if="row.dataBits === opt" class="menu-tick">✓</span>
                        </button>
                      </div>
                    </Transition>
                  </div>
                </div>

                <div class="draft-cell">
                  <span class="draft-label">停止位</span>
                  <div class="field-wrap">
                    <button @click="toggleDeviceMenu(idx + ':stopBits')" class="field field--btn">
                      <span>{{ deviceStopBitsLabel(row.stopBits) }}</span>
                      <svg class="chev" :class="{ 'is-open': deviceMenu === idx + ':stopBits' }" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round"><polyline points="6 9 12 15 18 9"></polyline></svg>
                    </button>
                    <div v-if="deviceMenu === idx + ':stopBits'" @click="deviceMenu = ''" class="fixed inset-0 z-0 cursor-default"></div>
                    <Transition name="slide-fade">
                      <div v-if="deviceMenu === idx + ':stopBits'" class="menu">
                        <button v-for="opt in deviceStopBitsOptions" :key="opt.value" @click="row.stopBits = opt.value; deviceMenu = ''" class="menu-item" :class="{ 'is-on': row.stopBits === opt.value }">
                          <span>{{ opt.label }}</span>
                          <span v-if="row.stopBits === opt.value" class="menu-tick">✓</span>
                        </button>
                      </div>
                    </Transition>
                  </div>
                </div>

                <div class="draft-cell">
                  <span class="draft-label">校验位</span>
                  <div class="field-wrap">
                    <button @click="toggleDeviceMenu(idx + ':parity')" class="field field--btn">
                      <span>{{ row.parity }}</span>
                      <svg class="chev" :class="{ 'is-open': deviceMenu === idx + ':parity' }" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round"><polyline points="6 9 12 15 18 9"></polyline></svg>
                    </button>
                    <div v-if="deviceMenu === idx + ':parity'" @click="deviceMenu = ''" class="fixed inset-0 z-0 cursor-default"></div>
                    <Transition name="slide-fade">
                      <div v-if="deviceMenu === idx + ':parity'" class="menu">
                        <button v-for="opt in parityOptions" :key="opt.value" @click="row.parity = opt.value; deviceMenu = ''" class="menu-item" :class="{ 'is-on': row.parity === opt.value }">
                          <span>{{ opt.label }}</span>
                          <span v-if="row.parity === opt.value" class="menu-tick">✓</span>
                        </button>
                      </div>
                    </Transition>
                  </div>
                </div>

                <div class="draft-cell draft-cell--full">
                  <div class="draft-cmd-head">
                    <span class="draft-label">上电</span>
                    <span v-if="row.powerOnHex" class="draft-hint">空格分隔的十六进制</span>
                  </div>
                  <div class="draft-cmd-line">
                    <input v-model="row.powerOn" placeholder="PWR-ON，如 0x01 0x02" class="field mono" />
                    <span class="draft-cap">HEX</span>
                    <button @click="row.powerOnHex = !row.powerOnHex" class="switch" :class="{ 'is-on': row.powerOnHex }" :title="row.powerOnHex ? '点击改为关' : '点击改为开'"><span class="switch-knob"></span><span class="switch-text">{{ row.powerOnHex ? '开' : '关' }}</span></button>
                    <span class="draft-cap">回车</span>
                    <button @click="row.powerOnEnter = !row.powerOnEnter" class="switch" :class="{ 'is-on': row.powerOnEnter }" :title="row.powerOnEnter ? '点击改为关' : '点击改为开'"><span class="switch-knob"></span><span class="switch-text">{{ row.powerOnEnter ? '开' : '关' }}</span></button>
                  </div>
                </div>

                <div class="draft-cell draft-cell--full">
                  <div class="draft-cmd-head">
                    <span class="draft-label">掉电</span>
                    <span v-if="row.powerOffHex" class="draft-hint">空格分隔的十六进制</span>
                  </div>
                  <div class="draft-cmd-line">
                    <input v-model="row.powerOff" placeholder="PWR-OFF，如 0xAA 0x00" class="field mono" />
                    <span class="draft-cap">HEX</span>
                    <button @click="row.powerOffHex = !row.powerOffHex" class="switch" :class="{ 'is-on': row.powerOffHex }" :title="row.powerOffHex ? '点击改为关' : '点击改为开'"><span class="switch-knob"></span><span class="switch-text">{{ row.powerOffHex ? '开' : '关' }}</span></button>
                    <span class="draft-cap">回车</span>
                    <button @click="row.powerOffEnter = !row.powerOffEnter" class="switch" :class="{ 'is-on': row.powerOffEnter }" :title="row.powerOffEnter ? '点击改为关' : '点击改为开'"><span class="switch-knob"></span><span class="switch-text">{{ row.powerOffEnter ? '开' : '关' }}</span></button>
                  </div>
                </div>
              </div>
            </div>

            <div v-if="deviceEditorError" class="alert">{{ deviceEditorError }}</div>
          </div>

          <div class="modal-foot">
            <div class="flex gap-1.5">
              <button @click="addDeviceDraft" class="btn">新增</button>
              <button @click="removeDeviceDraft" class="btn">删除</button>
            </div>
            <div class="flex gap-1.5">
              <button @click="cancelDeviceEditor" class="btn">取消</button>
              <button @click="saveDeviceEditor" :disabled="deviceSaving" class="btn btn-primary">
                {{ deviceSaving ? '保存中…' : '保存' }}
              </button>
            </div>
          </div>
          <div class="modal-note truncate" :title="pwrDevPath">配置文件：{{ pwrDevPath || 'pwr_dev.ini（与 exe 同目录）' }}</div>
        </div>
      </div>
    </Transition>

    <!-- TASK：数值编辑弹窗（等待时间 / 延时时间 / 运行次数） -->
    <Transition name="modal-fade">
      <div v-if="numberDialog.show" class="modal-backdrop modal-backdrop--top">
        <div class="modal modal--number" @click.stop>
          <div class="modal-head">
            <div class="modal-title">{{ numberDialog.title }}</div>
            <button @click="cancelNumberDialog" class="modal-x" title="关闭"><svg viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round"><line x1="18" y1="6" x2="6" y2="18"></line><line x1="6" y1="6" x2="18" y2="18"></line></svg></button>
          </div>
          <div class="modal-body modal-body--row">
            <input v-model.number="numberDialog.value" type="number" :min="numberDialog.min" :max="numberDialog.max"
                   class="field mono" @keydown.enter.prevent="confirmNumberDialog" />
            <span class="unit">{{ numberDialog.unit }}</span>
          </div>
          <div class="modal-foot modal-foot--end">
            <button @click="cancelNumberDialog" class="btn">取消</button>
            <button @click="confirmNumberDialog" class="btn btn-primary">保存</button>
          </div>
        </div>
      </div>
    </Transition>

    <!-- AUTO CMD：命令文本编辑弹窗 -->
    <Transition name="modal-fade">
      <div v-if="autoCmdDialog.show" class="modal-backdrop modal-backdrop--top">
        <div class="modal modal--cmd" @click.stop>
          <div class="modal-head">
            <div class="modal-title">
              {{ autoCmdDialog.index >= 0 ? `编辑命令 ${autoCmdDialog.index + 1}` : '编辑命令' }}
            </div>
            <button @click="cancelAutoCmdDialog" class="modal-x" title="关闭"><svg viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round"><line x1="18" y1="6" x2="6" y2="18"></line><line x1="6" y1="6" x2="18" y2="18"></line></svg></button>
          </div>

          <div class="modal-body">
            <div class="cmd-edit-row">
              <!-- 点击标志在 TX / RX 之间切换：TX 绿色，RX 红色 -->
              <button @click="toggleDialogCmdType"
                      class="type-toggle"
                      :class="autoCmdDialog.type === 'RX' ? 'is-rx' : 'is-tx'"
                      :title="`点击切换为 ${autoCmdDialog.type === 'TX' ? 'RX' : 'TX'}`">
                {{ autoCmdDialog.type }}
              </button>
              <!-- HEX / 回车：只对 TX 生效；RX 下只置灰展示默认值，已有的 TX 值留在模型里等保存时归一化 -->
              <div class="flag-col">
                <div class="flag-row" :class="{ 'is-off': autoCmdDialog.type === 'RX' }">
                  <span class="flag-label">HEX</span>
                  <button @click="autoCmdDialog.hex = !autoCmdDialog.hex"
                          class="switch"
                          :class="{ 'is-on': hexChecked }"
                          :disabled="autoCmdDialog.type === 'RX'"
                          :title="autoCmdDialog.type === 'RX' ? 'RX 是文本匹配，不支持 HEX' : (autoCmdDialog.hex ? '点击改为关' : '点击改为开')"><span class="switch-knob"></span><span class="switch-text">{{ hexChecked ? '开' : '关' }}</span></button>
                </div>
                <div class="flag-row" :class="{ 'is-off': autoCmdDialog.type === 'RX' }">
                  <span class="flag-label">回车</span>
                  <button @click="autoCmdDialog.enter = !autoCmdDialog.enter"
                          class="switch"
                          :class="{ 'is-on': dialogEnterOn }"
                          :disabled="autoCmdDialog.type === 'RX'"
                          :title="autoCmdDialog.type === 'RX' ? 'RX 是文本匹配，不发送数据' : (autoCmdDialog.enter ? '点击改为关' : '点击改为开')"><span class="switch-knob"></span><span class="switch-text">{{ dialogEnterOn ? '开' : '关' }}</span></button>
                </div>
              </div>
              <textarea v-model="autoCmdDialog.text" rows="3" ref="autoCmdInputRef"
                        class="field field--area mono"
                        placeholder="输入一条命令，例如 AT+RST"
                        @keydown.ctrl.enter.prevent="confirmAutoCmdDialog"
                        @keydown.enter.stop></textarea>
              <!-- 行尾数字 = 这条命令之后的延时：固定两位（00 ~ 99），超过两位提示有误 -->
              <div class="num-col">
                <input ref="autoCmdNumberRef"
                       :value="autoCmdDialog.numberText"
                       type="text" inputmode="numeric" maxlength="2"
                       class="field mono num-input"
                       :class="autoCmdDialog.numberError ? 'is-error' : ''"
                       placeholder="00"
                       title="延时秒数，两位（00 ~ 99）"
                       @input="onCmdNumberInput"
                       @keydown.enter.prevent="onCmdNumberEnter" />
                <span class="num-label">延时</span>
              </div>
            </div>

            <!-- HEX 预览：按字节还原成 AA BB CC，并补上回车开关实际会追加的 0D；内容非法时改显示错误并禁止保存 -->
            <p v-if="hexInvalid" class="err-line">不是合法的十六进制</p>
            <p v-else-if="hexChecked" class="hex-preview">
              <span class="hex-preview-cap">发送字节</span>
              <span class="mono">{{ hexPreview.bytes ? `${hexPreview.bytes}${hexPreview.autoCr ? ' 0D' : ''}` : '（空）' }}</span>
            </p>
            <p v-if="autoCmdDialog.numberError" class="err-line">{{ autoCmdDialog.numberError }}</p>

            <!-- 功能键插入条：点一下就把标记插到光标处，发送时由后端换成实际按键字节；HEX 模式下后端不解析标记，直接置灰 -->
            <div class="key-row">
              <span class="key-label">功能键</span>
              <button v-for="k in cmdKeyButtons" :key="k.token" type="button"
                      @click="insertCmdKey(k.token)"
                      class="key-chip mono"
                      :disabled="hexChecked"
                      :title="hexChecked ? 'HEX 模式下功能键标记不生效' : k.tip">{{ k.label }}</button>
            </div>

            <p class="hint-line">点左侧标志可在 TX / RX 之间切换；「延时」是这条命令之后等待的秒数，只能两位（00 ~ 99）；Ctrl + Enter 保存；清空内容后保存会删除这一条。</p>
            <p class="hint-line">HEX 打开后按空格分隔的十六进制字节发送（如 <span class="mono">AA BB CC</span>，可带 0x），此时功能键不可用；回车开着时末尾会自动补 0D，关掉就不再补。功能键标记会原样保存在命令里（如 <span class="mono">AT+RST{ENTER}{UP}</span>），发送时替换成实际按键；末尾没有 {ENTER}/{LF} 时 TX 会自动补一个回车；{0x1B} 可发任意单字节。</p>
          </div>

          <div class="modal-foot modal-foot--end">
            <button @click="cancelAutoCmdDialog" class="btn">取消</button>
            <button @click="confirmAutoCmdDialog" :disabled="hexInvalid" class="btn btn-primary">保存</button>
          </div>
        </div>
      </div>
    </Transition>

    <!-- 自定义弹窗 (Modal) -->
    <Transition name="modal-fade">
      <div v-if="modal.show" class="modal-backdrop">
        <div class="modal modal--msg" @click.stop>
          <div class="modal-head">
            <div class="modal-title" :class="modal.type === 'error' ? 'is-error' : ''">
              <svg v-if="modal.type === 'error'" class="modal-ico" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2.5" stroke-linecap="round" stroke-linejoin="round"><circle cx="12" cy="12" r="10"></circle><line x1="12" y1="8" x2="12" y2="12"></line><line x1="12" y1="16" x2="12.01" y2="16"></line></svg>
              <svg v-else class="modal-ico" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2.5" stroke-linecap="round" stroke-linejoin="round"><circle cx="12" cy="12" r="10"></circle><line x1="12" y1="16" x2="12" y2="12"></line><line x1="12" y1="8" x2="12.01" y2="8"></line></svg>
              <span>{{ modal.title }}</span>
            </div>
            <button @click="closeModal" class="modal-x" title="关闭"><svg viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round"><line x1="18" y1="6" x2="6" y2="18"></line><line x1="6" y1="6" x2="18" y2="18"></line></svg></button>
          </div>
          <div class="modal-body">
            <p class="modal-msg mono custom-scrollbar">{{ modal.message }}</p>
          </div>
          <div class="modal-foot modal-foot--end">
            <button @click="closeModal" class="btn btn-primary btn-wide">确定</button>
          </div>
        </div>
      </div>
    </Transition>


  </div>
</template>

<style scoped>
/* ============================================================
   SerialMateX — 视觉系统
   只负责外观：颜色/字号/间距/圆角/描边/阴影/动效。
   所有状态仍由既有的 v-if / :class 驱动，不改任何操作逻辑。
   ============================================================ */
.app-root {
  font-family: "Segoe UI Variable Text", "Segoe UI", system-ui, -apple-system, "PingFang SC", "Microsoft YaHei", sans-serif;
  font-size: 13px;
  line-height: 1.45;
  color: var(--text-main);
  background-color: var(--bg-main);
  background-repeat: no-repeat;
  background-size: cover;
  -webkit-font-smoothing: antialiased;
  text-rendering: optimizeLegibility;
}
.app-root ::selection {
  background: color-mix(in srgb, var(--col-primary) 24%, transparent);
}
.app-root button {
  font: inherit;
  cursor: pointer;
  -webkit-tap-highlight-color: transparent;
}
.app-root :focus-visible {
  outline: 2px solid var(--col-primary);
  outline-offset: 2px;
}
.mono {
  font-family: "Cascadia Mono", "Cascadia Code", "JetBrains Mono", Consolas, ui-monospace, monospace;
  font-variant-numeric: tabular-nums;
  letter-spacing: 0;
}

/* ---------------- 侧栏骨架 ---------------- */
.rail {
  position: relative;
  flex: 0 0 288px;
  width: 288px;
  display: flex;
  flex-direction: column;
  min-height: 0;
  background: var(--bg-side);
}
.rail--l { border-right: 1px solid var(--line); }
.rail--r { border-left: 1px solid var(--line); }

.rail-head {
  flex: none;
  height: 54px;
  padding: 0 10px 0 14px;
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: 8px;
  border-bottom: 1px solid var(--line);
}
.brand { display: flex; align-items: center; gap: 9px; min-width: 0; }
.brand-mark {
  flex: none;
  width: 24px;
  height: 24px;
  display: grid;
  place-items: center;
  border-radius: 6px;
  color: #fff;
  background: var(--col-primary);
  box-shadow: inset 0 1px 0 rgba(255, 255, 255, 0.22), 0 1px 2px rgba(0, 0, 0, 0.14);
}
.brand-name {
  font-size: 14px;
  font-weight: 650;
  letter-spacing: -0.01em;
  color: var(--text-main);
  white-space: nowrap;
}
.rail-actions { display: flex; align-items: center; gap: 2px; }

.icon-btn {
  flex: none;
  width: 28px;
  height: 28px;
  display: grid;
  place-items: center;
  border-radius: 7px;
  border: 1px solid transparent;
  color: var(--text-sub);
  background: transparent;
  transition: color 0.14s, background 0.14s, border-color 0.14s, transform 0.12s;
}
.icon-btn:hover { color: var(--text-main); background: var(--hover); }
.icon-btn:active { transform: translateY(1px); }
.icon-btn.is-on {
  color: var(--text-main);
  background: var(--card);
  border-color: var(--line);
  box-shadow: var(--shadow-xs);
}
.icon-btn--square { width: 32px; height: 32px; border-color: var(--line); background: var(--card); }
.icon-btn--square:hover { border-color: var(--line-2); }

/* ---------------- 弹出层（换肤 / 关于） ---------------- */
.backdrop {
  position: fixed;
  inset: 0;
  z-index: 20;
}
.popover {
  position: absolute;
  top: 50px;
  left: 10px;
  right: 10px;
  z-index: 30;
  display: flex;
  flex-direction: column;
  max-height: calc(100% - 62px);
  min-height: 0;
  background: var(--card);
  border: 1px solid var(--line);
  border-radius: 12px;
  box-shadow: var(--shadow-lg);
  overflow: hidden auto;
  backdrop-filter: blur(var(--blur));
}
.pop-head {
  display: flex;
  align-items: center;
  gap: 9px;
  padding: 11px 12px;
  border-bottom: 1px solid var(--line);
}
.pop-mark {
  flex: none;
  width: 26px;
  height: 26px;
  display: grid;
  place-items: center;
  border-radius: 7px;
  color: var(--col-primary);
  background: var(--accent-soft);
}
.pop-title { font-size: 12px; font-weight: 650; color: var(--text-main); }
.pop-sub { font-size: 10px; color: var(--text-sub); margin-top: 1px; }

.skin-list { padding: 6px; display: flex; flex-direction: column; gap: 1px; }
.skin-item {
  display: flex;
  align-items: center;
  gap: 9px;
  width: 100%;
  padding: 7px 8px;
  border: 1px solid transparent;
  border-radius: 8px;
  text-align: left;
  transition: background 0.14s, border-color 0.14s;
}
.skin-item:hover { background: var(--hover); }
.skin-item.is-on { background: var(--accent-soft); border-color: var(--line-2); }
.swatches { display: flex; flex: none; }
.swatch {
  width: 11px;
  height: 11px;
  border-radius: 3px;
  border: 1.5px solid var(--card);
  box-shadow: 0 0 0 1px var(--line);
}
.swatch + .swatch { margin-left: -3px; }
.skin-text { flex: 1; min-width: 0; display: flex; flex-direction: column; }
.skin-name { font-size: 11.5px; font-weight: 600; color: var(--text-main); }
.skin-desc {
  font-size: 10px;
  color: var(--text-sub);
  white-space: nowrap;
  overflow: hidden;
  text-overflow: ellipsis;
}
.skin-tick { flex: none; color: var(--col-primary); }

.version-card {
  margin: 12px;
  padding: 10px;
  border: 1px solid var(--line);
  border-radius: 8px;
  background: var(--field);
  display: flex;
  flex-direction: column;
  gap: 8px;
}
.version-row { display: flex; align-items: center; justify-content: space-between; }
.version-pill {
  font-family: "Cascadia Mono", Consolas, monospace;
  font-size: 10.5px;
  font-weight: 600;
  padding: 2px 6px;
  border-radius: 5px;
  color: var(--text-main);
  background: var(--card);
  border: 1px solid var(--line);
}
.update-card {
  margin: 0 12px 12px;
  padding: 10px;
  border: 1px solid color-mix(in srgb, var(--col-primary) 30%, var(--line));
  border-radius: 8px;
  background: var(--accent-soft);
  display: flex;
  flex-direction: column;
  gap: 8px;
}
.update-head { display: flex; align-items: center; gap: 6px; }
.badge-new {
  font-size: 8.5px;
  font-weight: 800;
  letter-spacing: 0.12em;
  padding: 2px 5px;
  border-radius: 4px;
  color: #fff;
  background: var(--col-primary);
}
.update-title { font-size: 11px; font-weight: 650; }
.update-versions { display: flex; align-items: center; gap: 6px; font-size: 11px; color: var(--text-sub); }
.update-next { color: var(--col-primary); font-weight: 700; }
.notes { display: flex; flex-direction: column; gap: 4px; }
.notes-body {
  font-size: 10.5px;
  line-height: 1.55;
  color: var(--text-sub);
  max-height: 92px;
  overflow-y: auto;
  white-space: pre-wrap;
}
.progress-row { display: flex; flex-direction: column; gap: 5px; }
.progress-meta { display: flex; align-items: center; justify-content: space-between; font-size: 9.5px; color: var(--text-sub); }
.progress-track { height: 3px; border-radius: 2px; background: var(--hover); overflow: hidden; }
.progress-fill { height: 100%; border-radius: 2px; background: var(--col-primary); transition: width 0.2s ease; }
.spinner {
  width: 12px;
  height: 12px;
  border-radius: 50%;
  border: 1.6px solid currentColor;
  border-top-color: transparent;
  animation: spin 0.7s linear infinite;
}
@keyframes spin { to { transform: rotate(360deg); } }

/* ---------------- 左栏内容 ---------------- */
.rail-body {
  flex: 1;
  min-height: 0;
  overflow-y: auto;
  padding: 12px;
  display: flex;
  flex-direction: column;
  gap: 10px;
}
.mode-row { display: flex; align-items: stretch; gap: 8px; }
.seg {
  flex: 1;
  display: flex;
  gap: 2px;
  padding: 2px;
  border-radius: 8px;
  border: 1px solid var(--line);
  background: var(--hover);
}
.seg > button {
  flex: 1;
  height: 26px;
  padding: 0 6px;
  border-radius: 6px;
  font-size: 10px;
  font-weight: 700;
  letter-spacing: 0.12em;
  text-transform: uppercase;
  color: var(--text-sub);
  transition: color 0.14s, background 0.14s, box-shadow 0.14s;
}
.seg > button:hover { color: var(--text-main); }
.seg > button.is-on {
  background: var(--card);
  color: var(--text-main);
  box-shadow: var(--shadow-xs);
}
/* 未开放的页签（Plot）置灰但保留位置，避免选项突然消失让人以为界面坏了 */
.seg > button:disabled { opacity: 0.45; cursor: not-allowed; }
.seg > button:disabled:hover { color: var(--text-sub); }
.seg--sub { margin-bottom: 2px; }
.seg--sub > button { letter-spacing: 0.06em; }
.seg--xs { flex: none; height: 24px; padding: 2px; gap: 1px; border-radius: 6px; }
.seg--xs > button { height: 18px; padding: 0 8px; font-size: 9.5px; letter-spacing: 0.08em; }

.panel {
  background: var(--card);
  border: 1px solid var(--line);
  border-radius: var(--radius);
  box-shadow: var(--shadow-xs);
}
.panel-head {
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: 8px;
  height: 34px;
  padding: 0 12px;
  border-bottom: 1px solid var(--line);
}
.panel-body {
  padding: 10px 12px 12px;
  display: flex;
  flex-direction: column;
  gap: 2px;
}
.eyebrow {
  font-size: 10px;
  font-weight: 650;
  letter-spacing: 0.14em;
  text-transform: uppercase;
  color: color-mix(in srgb, var(--text-main) 62%, transparent);
  white-space: nowrap;
}
.micro {
  font-size: 9.5px;
  font-weight: 700;
  letter-spacing: 0.14em;
  text-transform: uppercase;
  color: var(--text-sub);
}
.conn-dot {
  flex: none;
  width: 7px;
  height: 7px;
  border-radius: 50%;
  background: var(--line-2);
  transition: background 0.2s, box-shadow 0.2s;
}
.conn-dot.is-on {
  background: #3f7d5b;
  box-shadow: 0 0 0 3px rgba(63, 125, 91, 0.16);
}

.field-stack { display: flex; flex-direction: column; gap: 8px; }
.field-row { display: flex; align-items: center; gap: 8px; }
.field-label {
  flex: 0 0 52px;
  font-size: 11.5px;
  color: color-mix(in srgb, var(--text-main) 68%, transparent);
  white-space: nowrap;
}
.field-wrap { position: relative; flex: 1; min-width: 0; }

.field {
  width: 100%;
  height: 30px;
  padding: 0 9px;
  border-radius: 7px;
  border: 1px solid var(--line);
  background: var(--field);
  color: var(--text-main);
  font-size: 12px;
  line-height: 28px;
  transition: border-color 0.14s, background 0.14s, box-shadow 0.14s;
}
.field:hover { border-color: var(--line-2); }
.field:focus {
  outline: none;
  border-color: var(--col-primary);
  background: var(--card);
  box-shadow: 0 0 0 3px var(--accent-ring);
}
.field:disabled,
.field.is-disabled { opacity: 0.55; cursor: not-allowed; }
.field::placeholder { color: var(--text-sub); opacity: 0.78; }
.field--btn {
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: 6px;
  text-align: left;
}
.chev {
  flex: none;
  width: 12px;
  height: 12px;
  color: var(--text-sub);
  transition: transform 0.18s ease;
}
.chev.is-open { transform: rotate(180deg); }

.menu {
  position: absolute;
  top: calc(100% + 4px);
  left: 0;
  right: 0;
  z-index: 50;
  padding: 4px;
  background: var(--card);
  border: 1px solid var(--line);
  border-radius: 8px;
  box-shadow: var(--shadow-md);
  max-height: 200px;
  overflow-y: auto;
}
.menu--up { top: auto; bottom: calc(100% + 4px); left: auto; right: 0; width: 132px; }
.menu--more { top: calc(100% + 6px); left: auto; right: 0; width: 132px; }
.menu-item {
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: 6px;
  width: 100%;
  padding: 6px 8px;
  border-radius: 5px;
  font-size: 12px;
  color: var(--text-main);
  text-align: left;
  transition: background 0.12s;
}
.menu-item:hover { background: var(--hover); }
.menu-item.is-on { background: var(--accent-soft); color: var(--col-primary); font-weight: 600; }
.menu-tick { font-size: 10px; color: var(--col-primary); }
.menu-empty { padding: 8px; font-size: 10.5px; text-align: center; color: var(--text-sub); }

.panel-sep {
  margin-top: 8px;
  padding-top: 8px;
  border-top: 1px solid var(--line);
  display: flex;
  flex-direction: column;
  gap: 2px;
}
.check-row {
  display: flex;
  align-items: center;
  gap: 7px;
  height: 26px;
  font-size: 11.5px;
  color: var(--text-main);
  cursor: pointer;
}
.check-row:hover { color: var(--col-primary); }
.check {
  width: 14px;
  height: 14px;
  margin: 0;
  accent-color: var(--col-primary);
  cursor: pointer;
}
.check-row--inline { height: auto; gap: 5px; font-size: 10.5px; color: var(--text-sub); }

.setting-row {
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: 8px;
  height: 28px;
}
.setting-label { font-size: 11.5px; color: var(--text-sub); }
.chip {
  display: inline-flex;
  align-items: center;
  justify-content: center;
  gap: 3px;
  min-width: 42px;
  height: 22px;
  padding: 0 7px;
  border-radius: 6px;
  border: 1px solid var(--line);
  background: var(--card);
  color: var(--text-sub);
  font-size: 11px;
  font-weight: 600;
  transition: background 0.14s, border-color 0.14s, color 0.14s;
}
.chip:hover:not(:disabled) { border-color: var(--line-2); color: var(--text-main); }
.chip.is-on {
  color: var(--col-primary);
  background: var(--accent-soft);
  border-color: color-mix(in srgb, var(--col-primary) 40%, transparent);
}
.chip.is-ok {
  color: #3f7d5b;
  background: rgba(63, 125, 91, 0.1);
  border-color: rgba(63, 125, 91, 0.32);
}
.chip.is-off { opacity: 0.7; }
.chip:disabled { cursor: not-allowed; }
.chip--num {
  min-width: 64px;
  height: 24px;
  padding: 0 8px;
  justify-content: flex-end;
  font-family: "Cascadia Mono", Consolas, monospace;
  font-size: 11.5px;
  color: var(--text-main);
}
.chip-unit { font-size: 9px; opacity: 0.55; }

.stat-row {
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: 8px;
  min-width: 0;
  height: 27px;
}
.stat-value {
  min-width: 0;
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
  text-align: right;
  font-family: "Cascadia Mono", Consolas, monospace;
  font-variant-numeric: tabular-nums;
  font-size: 12px;
  font-weight: 600;
  color: var(--text-main);
}
.stat-sub { font-size: 9px; color: var(--text-sub); margin: 0 1px; }
.stat-accent { color: var(--col-primary); }
.stat-ok { color: #3f7d5b; }
.status-pill {
  font-size: 8.5px;
  font-weight: 800;
  letter-spacing: 0.12em;
  padding: 2px 5px;
  border-radius: 4px;
  border: 1px solid var(--line);
  background: var(--hover);
  color: var(--text-sub);
}
.status-pill.is-live {
  color: #3f7d5b;
  background: rgba(63, 125, 91, 0.12);
  border-color: rgba(63, 125, 91, 0.32);
}
/* 异常暂停提示：整行横幅，避免长句被状态列的窄格裁掉 */
.paused-note {
  display: flex;
  align-items: center;
  gap: 6px;
  margin-top: 8px;
  padding: 5px 8px;
  border: 1px solid color-mix(in srgb, var(--col-error) 30%, transparent);
  border-radius: 7px;
  background: color-mix(in srgb, var(--col-error) 8%, transparent);
  color: var(--col-error);
  font-size: 10px;
  font-weight: 650;
  line-height: 1.35;
}
.paused-note__dot {
  flex: none;
  width: 5px;
  height: 5px;
  border-radius: 50%;
  background: var(--col-error);
}
.status-pill.is-warn {
  color: var(--col-error);
  background: color-mix(in srgb, var(--col-error) 10%, transparent);
  border-color: color-mix(in srgb, var(--col-error) 34%, transparent);
  font-size: 9.5px;
  font-weight: 700;
  letter-spacing: 0.02em;
  padding: 3px 7px;
}

/* ---------------- 按钮 ---------------- */
.btn {
  display: inline-flex;
  align-items: center;
  justify-content: center;
  gap: 6px;
  height: 28px;
  padding: 0 10px;
  border-radius: 7px;
  border: 1px solid var(--line);
  background: var(--card);
  color: var(--text-main);
  font-size: 11.5px;
  font-weight: 600;
  white-space: nowrap;
  transition: background 0.14s, border-color 0.14s, color 0.14s, transform 0.12s, box-shadow 0.14s;
}
.btn:hover:not(:disabled) { background: var(--hover); border-color: var(--line-2); }
.btn:active:not(:disabled) { transform: translateY(1px); }
.btn:disabled { opacity: 0.42; cursor: not-allowed; }
.btn-primary {
  color: var(--on-primary, #fff);
  background: var(--col-primary);
  border-color: transparent;
  box-shadow: inset 0 1px 0 rgba(255, 255, 255, 0.18), var(--shadow-xs);
}
.btn-primary:hover:not(:disabled) {
  background: color-mix(in srgb, var(--col-primary) 88%, #000);
  border-color: transparent;
}
.btn-warn {
  color: var(--on-error, #fff);
  background: var(--col-error);
  border-color: transparent;
}
.btn-warn:hover:not(:disabled) { background: color-mix(in srgb, var(--col-error) 88%, #000); border-color: transparent; }
.btn-quiet {
  background: transparent;
  border-style: dashed;
  color: var(--text-sub);
  font-weight: 500;
  box-shadow: none;
}
.btn-quiet:hover:not(:disabled) { background: var(--hover); color: var(--text-main); border-style: dashed; }
.btn-block { width: 100%; }
.btn-wide { min-width: 88px; }
.btn-xs { height: 22px; padding: 0 7px; font-size: 10.5px; font-weight: 600; border-radius: 5px; }
.btn-ghost {
  color: var(--text-sub);
  background: transparent;
  border-color: transparent;
  box-shadow: none;
}
.btn-ghost:hover:not(:disabled) { color: var(--text-main); background: var(--hover); border-color: transparent; }
.btn-accent {
  color: var(--col-primary);
  background: var(--accent-soft);
  border-color: color-mix(in srgb, var(--col-primary) 26%, transparent);
  box-shadow: none;
}
.btn-accent:hover:not(:disabled) {
  background: color-mix(in srgb, var(--col-primary) 16%, transparent);
  border-color: color-mix(in srgb, var(--col-primary) 40%, transparent);
}
.btn-device {
  height: 30px;
  margin-bottom: 6px;
  justify-content: center;
  gap: 7px;
  font-size: 11.5px;
  font-weight: 500;
  color: var(--text-main);
  background: var(--field);
}
.btn-device:hover:not(:disabled) { background: var(--hover); }

/* 布尔开关：轨道 + 圆钮，开时轨道填主色 */
.switch {
  display: inline-flex;
  align-items: center;
  gap: 7px;
  height: 22px;
  padding: 0 6px 0 3px;
  border-radius: 6px;
  border: 1px solid transparent;
  background: transparent;
  transition: background 0.14s, border-color 0.14s;
}
.switch:hover:not(:disabled) { background: var(--hover); }
.switch-knob {
  position: relative;
  width: 26px;
  height: 15px;
  border-radius: 8px;
  border: 1px solid var(--line-2);
  background: var(--field);
  transition: background 0.16s ease, border-color 0.16s ease;
}
.switch-knob::after {
  content: "";
  position: absolute;
  top: 1px;
  left: 1px;
  width: 11px;
  height: 11px;
  border-radius: 50%;
  background: var(--card);
  box-shadow: 0 1px 2px rgba(24, 27, 21, 0.28);
  transition: transform 0.16s cubic-bezier(0.22, 1, 0.36, 1);
}
.switch.is-on .switch-knob {
  background: var(--col-primary);
  border-color: transparent;
}
.switch.is-on .switch-knob::after { transform: translateX(11px); }
.switch-text { font-size: 10.5px; font-weight: 600; color: var(--text-sub); }
.switch.is-on .switch-text { color: var(--col-primary); }
.switch:disabled { cursor: not-allowed; opacity: 0.5; }
.btn-connect {
  width: 100%;
  height: 38px;
  border-radius: 8px;
  font-size: 12px;
  font-weight: 700;
  letter-spacing: 0.24em;
  text-indent: 0.24em;
}
.btn-connect.is-connected {
  color: var(--text-main);
  background: var(--card);
  border-color: var(--line-2);
  box-shadow: none;
}
.btn-connect.is-connected:hover:not(:disabled) { background: var(--hover); }
.btn-connect.is-disabled { opacity: 0.45; }
.dot { flex: none; width: 5px; height: 5px; border-radius: 50%; background: currentColor; }
.dot--pulse { animation: dot-pulse 1.8s ease-in-out infinite; }
@keyframes dot-pulse {
  0%, 100% { opacity: 0.35; transform: scale(0.85); }
  50% { opacity: 1; transform: scale(1); }
}
.rail-foot {
  flex: none;
  padding: 10px 12px 12px;
  border-top: 1px solid var(--line);
}

/* ---------------- 中栏 ---------------- */
.stage {
  flex: 1;
  min-width: 0;
  min-height: 0;
  display: flex;
  flex-direction: column;
  gap: 8px;
  padding: 12px;
}
.rx-panel {
  flex: 1;
  min-height: 0;
  display: flex;
  flex-direction: column;
  overflow: hidden;
}
.panel-bar {
  flex: none;
  height: 42px;
  padding: 0 8px 0 14px;
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: 10px;
  border-bottom: 1px solid var(--line);
}
.bar-group { display: flex; align-items: center; gap: 10px; min-width: 0; }
.bar-group--end { margin-left: auto; gap: 8px; }
.count-pill {
  flex: none;
  font-family: "Cascadia Mono", Consolas, monospace;
  font-size: 9.5px;
  padding: 2px 6px;
  border-radius: 4px;
  color: var(--text-sub);
  background: var(--hover);
  border: 1px solid var(--line);
}
.bar-hint {
  font-size: 10px;
  color: var(--text-sub);
  white-space: nowrap;
  overflow: hidden;
  text-overflow: ellipsis;
}
.clear-btn { width: 26px; height: 26px; }
.rx-text {
  flex: 1;
  min-height: 0;
  width: 100%;
  background: color-mix(in srgb, var(--field) 62%, var(--card));
  padding: 14px 16px;
  border: 0;
  outline: none;
  resize: none;
  color: var(--text-main);
  font-family: "Cascadia Mono", "Cascadia Code", Consolas, ui-monospace, monospace;
  font-size: 12px;
  line-height: 1.72;
  cursor: text;
}
.rx-text::placeholder { color: var(--text-sub); opacity: 0.82; }

/* 行视图：RX 逐行铺开，TX（任务发出的命令）用主色加粗并带发送时间；Hex 模式仍用上面的 .rx-text */
.rx-log {
  flex: 1;
  min-height: 0;
  width: 100%;
  overflow: auto;
  background: color-mix(in srgb, var(--field) 62%, var(--card));
  padding: 12px 16px;
  border: 0;
  outline: none;
  color: var(--text-main);
  font-family: "Cascadia Mono", "Cascadia Code", Consolas, ui-monospace, monospace;
  font-size: 12px;
  line-height: 1.72;
  cursor: text;
  /* 根节点是 select-none（避免拖窗口时选中整页），接收区要能选文字才能 Ctrl+C 复制 */
  -webkit-user-select: text;
  user-select: text;
}
/* 三栏面板本身有 1px 描边，接收区再套 2px 焦点环只会漏出顶边一条线（左右下都被面板裁掉）；
   focus-visible 又因为 role=textbox 在键入时必然命中，所以这里显式关掉，避免"标题框下面出现一根线" */
.rx-log:focus,
.rx-log:focus-visible,
.rx-text:focus,
.rx-text:focus-visible {
  outline: none;
}
.rx-line {
  min-height: 1.72em; /* 空行也要占一行高度，否则接收里的空行会被吃掉 */
  white-space: pre-wrap;
  word-break: break-all;
}
.rx-line.is-tx { color: var(--col-primary); font-weight: 600; }
.rx-tag { opacity: .55; margin-right: 8px; font-size: 10.5px; }
.rx-empty {
  color: var(--text-sub);
  opacity: .82;
  /* 空态提示不参与选择：否则日志为空时 Ctrl+A / Ctrl+C 会把它当成"有选中"而不发 0x03 */
  -webkit-user-select: none;
  user-select: none;
}
.chart-wrap { flex: 1; min-height: 0; padding: 8px; }
.chart { width: 100%; height: 100%; }

.tx {
  flex: none;
  transition: height 0.26s cubic-bezier(0.22, 1, 0.36, 1), opacity 0.18s ease;
}
.tx.is-open { height: 84px; opacity: 1; }
.tx.is-closed { height: 0; opacity: 0; overflow: hidden; pointer-events: none; }
.tx-card { height: 100%; display: flex; flex-direction: column; overflow: visible; }
.tx-bar {
  flex: none;
  height: 36px;
  padding: 0 10px 0 14px;
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: 8px;
  border-bottom: 1px solid var(--line);
}
.vrule { flex: none; width: 1px; height: 13px; background: var(--line); }
.eol-btn {
  display: inline-flex;
  align-items: center;
  gap: 5px;
  height: 22px;
  padding: 0 6px;
  border-radius: 5px;
  border: 1px solid transparent;
  color: var(--text-sub);
  transition: background 0.14s, color 0.14s, border-color 0.14s;
}
.eol-btn:hover { background: var(--hover); color: var(--text-main); }
.eol-btn.is-on { background: var(--hover); color: var(--text-main); border-color: var(--line); }
.eol-label { font-size: 9px; font-weight: 700; letter-spacing: 0.1em; opacity: 0.75; }
.eol-value { font-size: 10.5px; color: var(--text-main); }
.is-muted { opacity: 0.45; }
.tx-input-row { flex: 1; display: flex; align-items: center; padding: 8px 10px; }
.tx-input {
  width: 100%;
  height: 34px;
  padding: 0 10px;
  border-radius: 6px;
  border: 1px solid var(--line);
  background: var(--field);
  color: var(--text-main);
  font-size: 12px;
  transition: border-color 0.14s, background 0.14s, box-shadow 0.14s;
}
.tx-input:hover { border-color: var(--line-2); }
.tx-input:focus {
  outline: none;
  border-color: var(--col-primary);
  background: var(--card);
  box-shadow: 0 0 0 3px var(--accent-ring);
}
.tx-input::placeholder { color: var(--text-sub); opacity: 0.6; }

.strip {
  flex: none;
  width: 100%;
  height: 22px;
  display: flex;
  align-items: center;
  justify-content: center;
  gap: 6px;
  border-radius: 6px;
  border: 1px solid var(--line);
  background: var(--card);
  color: var(--text-sub);
  font-size: 9px;
  font-weight: 700;
  letter-spacing: 0.2em;
  transition: background 0.14s, color 0.14s, border-color 0.14s;
}
.strip:hover { background: var(--hover); color: var(--text-main); border-color: var(--line-2); }

/* ---------------- 右栏 AUTO CMD ---------------- */
.cmd-head {
  flex: none;
  height: 52px;
  padding: 0 8px 0 14px;
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: 8px;
  border-bottom: 1px solid var(--line);
}
.cmd-head-title { display: flex; align-items: center; gap: 6px; min-width: 0; }
.cmd-actions { display: flex; align-items: center; gap: 2px; }
.cmd-list {
  flex: 1;
  min-height: 0;
  overflow-y: auto;
  padding: 8px;
  display: flex;
  flex-direction: column;
  gap: 2px;
}
.cmd-list.is-locked { pointer-events: none; opacity: 0.7; }
.cmd-row {
  display: flex;
  align-items: flex-start;
  gap: 7px;
  width: 100%;
  padding: 5px 7px;
  border-radius: 7px;
  border: 1px solid transparent;
  text-align: left;
  transition: background 0.14s, border-color 0.14s, box-shadow 0.14s;
}
.cmd-row:hover { background: var(--hover); }
.cmd-row.is-selected {
  background: var(--card);
  border-color: var(--line-2);
  box-shadow: inset 2px 0 0 var(--col-primary), var(--shadow-xs);
}
.cmd-row.is-live {
  background: var(--accent-soft);
  border-color: color-mix(in srgb, var(--col-primary) 45%, transparent);
}
.cmd-row.is-live.is-wait {
  background: rgba(63, 125, 91, 0.12);
  border-color: rgba(63, 125, 91, 0.42);
}
.cmd-num {
  flex: none;
  width: 17px;
  height: 17px;
  margin-top: 1px;
  display: grid;
  place-items: center;
  border-radius: 4px;
  background: var(--hover);
  color: var(--text-sub);
  font-size: 9.5px;
  font-weight: 700;
}
.cmd-type {
  flex: none;
  width: 19px;
  margin-top: 1px;
  font-size: 8.5px;
  font-weight: 800;
  letter-spacing: 0.06em;
  line-height: 17px;
}
.cmd-type.is-tx { color: #3f7d5b; }
.cmd-type.is-rx { color: #b3564e; }
.cmd-text {
  flex: 1;
  min-width: 0;
  font-size: 11px;
  line-height: 17px;
  color: var(--text-main);
  word-break: break-all;
}
.cmd-count {
  flex: none;
  min-width: 24px;
  height: 17px;
  margin-top: 1px;
  display: grid;
  place-items: center;
  border-radius: 4px;
  background: var(--hover);
  color: var(--text-sub);
  font-size: 9.5px;
}
.cmd-count.is-live { background: var(--col-primary); color: var(--on-primary, #fff); font-weight: 700; }
.cmd-empty {
  padding: 26px 12px;
  display: flex;
  flex-direction: column;
  align-items: center;
  gap: 3px;
  font-size: 11px;
  color: var(--text-sub);
  text-align: center;
}
.run-foot { flex: none; padding: 10px 12px 12px; border-top: 1px solid var(--line); }

/* AUTO CMD 底部按钮的外框：
   - 停止：外圈呼吸灯（缓慢明暗）
   - 运行：SVG 描边 + pathLength 归一化，沿整圈边框按弧长匀速流动，长边短边速度一致 */
.run-ring {
  position: relative;
  padding: 3px;
  border-radius: 12px;
  transition: background-color 0.3s ease, box-shadow 0.3s ease;
}
.run-ring::before {
  content: "";
  position: absolute;
  inset: 0;
  border-radius: 12px;
  background: color-mix(in srgb, var(--col-primary) 26%, transparent);
  transition: background 0.3s ease;
}
.run-ring--idle::before { background: color-mix(in srgb, var(--col-primary) 7%, transparent); }
.run-ring--running {
  box-shadow: 0 0 12px color-mix(in srgb, var(--col-primary) 30%, transparent);
}
.run-ring--running::before { background: color-mix(in srgb, var(--col-primary) 10%, transparent); }
/* 异常暂停：外框换成静态描边（不呼吸、不流光），里面并排“结束任务 / 跳过本轮” */
.run-ring--paused {
  box-sizing: border-box;
  border: 1px solid color-mix(in srgb, var(--col-error) 36%, transparent);
  box-shadow: none;
  animation: none;
}
.run-ring--paused::before {
  background: color-mix(in srgb, var(--col-error) 7%, transparent);
  animation: none;
}
.run-foot--paused .run-ring {
  display: grid;
  grid-template-columns: 1fr 1fr;
  gap: 7px;
}
.run-foot--paused .btn {
  position: relative;
  z-index: 1;
  height: 42px;
  border-radius: 9px;
  font-size: 12px;
  font-weight: 700;
  letter-spacing: 0.08em;
}
.run-ring__svg {
  position: absolute;
  inset: 0;
  width: 100%;
  height: 100%;
  pointer-events: none;
  border-radius: 12px;
  overflow: visible;
}
.run-ring__dash { animation: run-ring-flow 2.4s linear infinite; }
@keyframes run-ring-flow {
  from { stroke-dashoffset: 100; }
  to { stroke-dashoffset: 0; }
}
@keyframes breath-ring {
  0%, 100% { opacity: 0.55; }
  50% { opacity: 1; }
}
.run-btn {
  position: relative;
  z-index: 1;
  width: 100%;
  height: 42px;
  border-radius: 9px;
  color: var(--on-primary, #fff);
  background: var(--col-primary);
  font-size: 12px;
  font-weight: 700;
  letter-spacing: 0.26em;
  text-indent: 0.26em;
  box-shadow: inset 0 1px 0 rgba(255, 255, 255, 0.18), var(--shadow-xs);
  transition: background 0.2s, opacity 0.2s, transform 0.12s;
}
.run-btn:hover:not(:disabled) { background: color-mix(in srgb, var(--col-primary) 88%, #000); }
.run-btn:active:not(:disabled) { transform: translateY(1px); }
.run-btn.is-running { background: var(--col-error); color: var(--on-error, #fff); }
.run-btn.is-running:hover:not(:disabled) { background: color-mix(in srgb, var(--col-error) 88%, #000); }
.run-btn.is-disabled {
  cursor: not-allowed;
  background: var(--field);
  color: color-mix(in srgb, var(--text-main) 46%, transparent);
  box-shadow: inset 0 0 0 1px var(--line);
}

/* ---------------- 弹窗 ---------------- */
.modal-backdrop {
  position: fixed;
  inset: 0;
  z-index: 50;
  display: grid;
  place-items: center;
  padding: 16px;
  background: rgba(20, 22, 20, 0.3);
  backdrop-filter: blur(2px);
}
.modal-backdrop--top { z-index: 60; }
.modal {
  display: flex;
  flex-direction: column;
  width: 560px;
  max-width: 94%;
  max-height: 86vh;
  background: var(--card);
  border: 1px solid var(--line);
  border-radius: 12px;
  box-shadow: var(--shadow-lg);
  overflow: hidden;
}
.modal--number { width: 300px; }
.modal--cmd { width: 540px; }
.modal--msg { width: 420px; }
.modal-head {
  flex: none;
  height: 44px;
  padding: 0 10px 0 14px;
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: 8px;
  border-bottom: 1px solid var(--line);
}
.modal-title {
  display: flex;
  align-items: center;
  gap: 7px;
  font-size: 12px;
  font-weight: 650;
  letter-spacing: 0.02em;
  color: var(--text-main);
}
.modal-title.is-error { color: var(--col-error); }
.modal-ico { width: 15px; height: 15px; color: var(--col-primary); }
.modal-title.is-error .modal-ico { color: var(--col-error); }
.modal-x {
  flex: none;
  width: 26px;
  height: 26px;
  display: grid;
  place-items: center;
  border-radius: 6px;
  color: var(--text-sub);
  transition: background 0.14s, color 0.14s;
}
.modal-x svg { width: 14px; height: 14px; }
.modal-x:hover { background: var(--hover); color: var(--text-main); }
.modal-body {
  flex: 1;
  min-height: 0;
  overflow-y: auto;
  padding: 12px;
  display: flex;
  flex-direction: column;
  gap: 8px;
}
.modal-body--row { flex-direction: row; align-items: center; gap: 8px; overflow: visible; }
.modal-foot {
  flex: none;
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: 8px;
  padding: 10px 12px;
  border-top: 1px solid var(--line);
}
.modal-foot--end { justify-content: flex-end; }
.modal-note {
  flex: none;
  padding: 0 12px 10px;
  font-size: 9.5px;
  color: var(--text-sub);
  opacity: 0.75;
}
.unit { flex: none; font-size: 11px; color: var(--text-sub); }
.draft-row {
  display: flex;
  flex-direction: column;
  gap: 6px;
  padding: 8px;
  border: 1px solid var(--line);
  border-radius: 8px;
  background: var(--field);
  cursor: pointer;
  transition: border-color 0.14s, background 0.14s;
}
.draft-row:hover { border-color: var(--line-2); }
.draft-row.is-on {
  border-color: color-mix(in srgb, var(--col-primary) 45%, transparent);
  background: var(--accent-soft);
}
.draft-head {
  display: flex;
  align-items: center;
  justify-content: space-between;
  font-size: 9.5px;
  font-weight: 700;
  letter-spacing: 0.1em;
  text-transform: uppercase;
  color: var(--text-sub);
}
.draft-tick { color: var(--col-primary); }
.draft-row .field { background: var(--card); }
.draft-grid { display: grid; grid-template-columns: 1fr 1fr; gap: 6px 6px; }
.draft-cell { display: flex; flex-direction: column; gap: 4px; min-width: 0; }
.draft-label {
  font-size: 9.5px;
  font-weight: 650;
  letter-spacing: 0.04em;
  color: var(--text-sub);
  padding-left: 2px;
}
/* 设备弹窗：8 项排布（4 个串口参数 + 上电/掉电各带 HEX/回车开关） */
.modal--devices { max-height: calc(100vh - 96px); }
.modal--devices .modal-body { overflow: auto; }
.draft-cell--full { grid-column: 1 / -1; }
.draft-cmd-head { display: flex; align-items: center; gap: 6px; }
.draft-hint { font-size: 9.5px; color: var(--text-sub); opacity: 0.8; }
.draft-cmd-line { display: flex; align-items: center; gap: 6px; }
.draft-cmd-line .field { flex: 1 1 auto; width: auto; min-width: 0; }
.draft-cap {
  flex: none;
  font-size: 9.5px;
  font-weight: 650;
  letter-spacing: 0.04em;
  color: var(--text-sub);
}
.draft-cmd-line .switch { flex: none; }
.alert {
  font-size: 10.5px;
  padding: 6px 8px;
  border-radius: 6px;
  color: var(--col-error);
  background: color-mix(in srgb, var(--col-error) 10%, transparent);
  border: 1px solid color-mix(in srgb, var(--col-error) 26%, transparent);
}
.cmd-edit-row { display: flex; align-items: flex-start; gap: 8px; }
/* 命令弹窗：HEX / 回车两个开关竖排在类型标志右边，RX 时整列置灰 */
.flag-col { flex: none; display: flex; flex-direction: column; gap: 2px; }
.flag-row { display: flex; align-items: center; gap: 2px; }
.flag-label {
  flex: none;
  width: 26px;
  font-size: 9px;
  font-weight: 650;
  letter-spacing: 0.04em;
  color: var(--text-sub);
}
.flag-col .switch { flex: none; }
/* RX 下只淡化标签：开关本身已由 .switch:disabled 的 .5 透明度处理，两层叠加会看不清 */
.flag-row.is-off .flag-label { opacity: 0.6; }
.type-toggle {
  flex: none;
  width: 42px;
  height: 30px;
  border-radius: 6px;
  border: 1px solid var(--line);
  font-size: 10px;
  font-weight: 800;
  letter-spacing: 0.08em;
  transition: background 0.14s, border-color 0.14s;
}
.type-toggle.is-tx {
  color: #3f7d5b;
  border-color: rgba(63, 125, 91, 0.35);
  background: rgba(63, 125, 91, 0.08);
}
.type-toggle.is-rx {
  color: #b3564e;
  border-color: rgba(179, 86, 78, 0.35);
  background: rgba(179, 86, 78, 0.08);
}
.field--area { flex: 1; height: auto; min-height: 62px; padding: 7px 9px; line-height: 1.6; resize: none; }
.field.is-error {
  border-color: var(--col-error);
  box-shadow: 0 0 0 3px color-mix(in srgb, var(--col-error) 18%, transparent);
}
.num-col { flex: none; width: 62px; display: flex; flex-direction: column; gap: 3px; }
.num-input { height: 30px; text-align: center; }
.num-label { font-size: 9px; text-align: center; color: var(--text-sub); }
.err-line { font-size: 10px; color: var(--col-error); }
/* HEX 预览行：等宽显示最终发送的字节，和错误行同一位置 */
.hex-preview {
  display: flex;
  align-items: baseline;
  gap: 6px;
  padding: 4px 8px;
  border-radius: 6px;
  border: 1px solid var(--line);
  background: var(--field);
  font-size: 10px;
  color: var(--text-main);
  word-break: break-all;
}
.hex-preview-cap { flex: none; font-size: 9px; color: var(--text-sub); }
.key-row { display: flex; flex-wrap: wrap; align-items: center; gap: 4px; margin-top: 2px; }
.key-label { font-size: 9px; color: var(--text-sub); margin-right: 2px; }
.key-chip {
  height: 20px;
  padding: 0 6px;
  border-radius: 4px;
  border: 1px solid var(--line);
  background: var(--card);
  color: var(--text-main);
  font-size: 9.5px;
  transition: background 0.14s, border-color 0.14s;
}
.key-chip:hover { background: var(--hover); border-color: var(--line-2); }
/* HEX 模式下后端不解析功能键标记，直接禁用这一排按钮 */
.key-chip:disabled { opacity: 0.4; cursor: not-allowed; }
.key-chip:disabled:hover { background: var(--card); border-color: var(--line); }
.hint-line { font-size: 10px; line-height: 1.55; color: var(--text-sub); }
.modal-msg {
  margin: 0;
  padding: 10px;
  border-radius: 6px;
  border: 1px solid var(--line);
  background: var(--field);
  color: var(--text-main);
  font-size: 11px;
  line-height: 1.65;
  max-height: 180px;
  overflow-y: auto;
  white-space: pre-wrap;
  word-break: break-word;
}

/* ---------------- 滚动条 ---------------- */
.custom-scrollbar::-webkit-scrollbar { width: 8px; height: 8px; }
.custom-scrollbar::-webkit-scrollbar-track { background: transparent; }
.custom-scrollbar::-webkit-scrollbar-thumb {
  background: var(--line-2);
  border-radius: 4px;
  border: 2px solid transparent;
  background-clip: padding-box;
}
.custom-scrollbar::-webkit-scrollbar-thumb:hover { background: var(--col-primary); background-clip: padding-box; }

/* ---------------- 过渡动画 ---------------- */
.slide-fade-enter-active { transition: all 0.18s cubic-bezier(0.16, 1, 0.3, 1); }
.slide-fade-leave-active { transition: all 0.12s ease-in; }
.slide-fade-enter-from,
.slide-fade-leave-to { transform: translateY(-4px); opacity: 0; }

.slide-down-enter-active { transition: all 0.26s cubic-bezier(0.16, 1, 0.3, 1); }
.slide-down-leave-active { transition: all 0.16s ease-in; }
.slide-down-enter-from,
.slide-down-leave-to { transform: translateY(-8px); opacity: 0; }

.backdrop-fade-enter-active { transition: opacity 0.22s ease-out; }
.backdrop-fade-leave-active { transition: opacity 0.16s ease-in; }
.backdrop-fade-enter-from,
.backdrop-fade-leave-to { opacity: 0; }

.dropdown-fade-enter-active { transition: all 0.16s cubic-bezier(0.16, 1, 0.3, 1); }
.dropdown-fade-leave-active { transition: all 0.12s ease-in; }
.dropdown-fade-enter-from,
.dropdown-fade-leave-to { transform: scale(0.96) translateY(-4px); opacity: 0; }

.modal-fade-enter-active,
.modal-fade-leave-active { transition: opacity 0.18s ease-out; }
.modal-fade-enter-from,
.modal-fade-leave-to { opacity: 0; }
.modal-fade-enter-from .modal,
.modal-fade-leave-to .modal { transform: scale(0.97); }
.modal { transition: transform 0.18s cubic-bezier(0.16, 1, 0.3, 1); }

/* 连接失败时的左右震动：让设置面板描边转红，明确指出问题在哪 */
@keyframes shake-x {
  0%, 100% { transform: translateX(0); }
  10%, 30%, 50%, 70%, 90% { transform: translateX(-3px); }
  20%, 40%, 60%, 80% { transform: translateX(3px); }
}
.shake-anim { animation: shake-x 0.42s cubic-bezier(0.36, 0.07, 0.19, 0.97) both; }
.shake-anim .panel {
  border-color: var(--col-error);
  box-shadow: 0 0 0 1px var(--col-error);
}

.broom-icon { transition: transform 0.18s ease; }
.clear-btn:hover .broom-icon { animation: broom-sweep 0.6s ease-in-out; }
.broom-icon.broom-clicked { animation: broom-sweep 0.6s ease-in-out; }
@keyframes broom-sweep {
  0%, 100% { transform: rotate(0deg) translateX(0); }
  25% { transform: rotate(-8deg) translateX(-2px); }
  50% { transform: rotate(8deg) translateX(2px); }
  75% { transform: rotate(-8deg) translateX(-2px); }
}

@media (prefers-reduced-motion: reduce) {
  .app-root *,
  .app-root *::before,
  .app-root *::after {
    animation-duration: 0.001ms !important;
    animation-iteration-count: 1 !important;
    transition-duration: 0.001ms !important;
  }
}

/* ============================================================
   皮肤：只覆盖背景装饰，颜色全部来自脚本里的 CSS 变量
   ============================================================ */
[data-skin="spatial"].app-root {
  background-image:
    radial-gradient(720px 460px at 10% -12%, rgba(255, 255, 255, 0.92), transparent 62%),
    radial-gradient(560px 360px at 98% 104%, rgba(47, 107, 88, 0.05), transparent 60%);
}
[data-skin="aurora"].app-root {
  background-image:
    radial-gradient(760px 520px at 8% -8%, rgba(139, 124, 255, 0.30), transparent 62%),
    radial-gradient(620px 460px at 92% 4%, rgba(90, 209, 200, 0.28), transparent 62%),
    radial-gradient(680px 520px at 62% 108%, rgba(255, 182, 217, 0.32), transparent 66%);
}
[data-skin="cyberpunk"].app-root {
  background-image:
    repeating-linear-gradient(0deg, rgba(0, 229, 255, 0.055) 0px, rgba(0, 229, 255, 0.055) 1px, transparent 1px, transparent 34px),
    repeating-linear-gradient(90deg, rgba(0, 229, 255, 0.045) 0px, rgba(0, 229, 255, 0.045) 1px, transparent 1px, transparent 34px),
    radial-gradient(700px 460px at 84% -6%, rgba(255, 46, 136, 0.22), transparent 62%),
    radial-gradient(680px 420px at 6% 104%, rgba(0, 229, 255, 0.20), transparent 64%);
}
[data-skin="ai-native"].app-root {
  background-image:
    radial-gradient(circle at 1px 1px, rgba(255, 255, 255, 0.055) 1px, transparent 0),
    radial-gradient(720px 480px at 88% -10%, rgba(167, 139, 250, 0.24), transparent 62%),
    radial-gradient(640px 420px at 4% 100%, rgba(52, 211, 153, 0.16), transparent 64%);
  background-size: 22px 22px, cover, cover;
}
[data-skin="bento"].app-root {
  background-image: linear-gradient(180deg, rgba(255, 255, 255, 0.85), rgba(245, 245, 240, 0.55));
}
[data-skin="soft"].app-root {
  background-image:
    radial-gradient(760px 520px at 14% -6%, rgba(255, 255, 255, 0.95), transparent 62%),
    radial-gradient(680px 460px at 96% 8%, rgba(163, 177, 198, 0.22), transparent 64%),
    radial-gradient(720px 520px at 50% 112%, rgba(255, 255, 255, 0.85), transparent 66%);
}
</style>
