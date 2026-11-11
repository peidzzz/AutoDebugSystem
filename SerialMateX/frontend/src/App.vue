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
import { GetSerialPorts, OpenSerial, OpenTcpClient, OpenTcpServer, OpenUdp, Close as CloseConnection, SendData, GetVersion, CheckForUpdates, DownloadAndInstallUpdate, QuitApp, GetPwrDevices, GetPwrDevPath, SavePwrDevices, GetTaskConfig, SaveTaskConfig, GetTaskCfgPath, GetAutoCmds, SaveAutoCmds, GetAutoCmdPath, StartTask, StopTask, ResumeTask, GetTaskRuntime, ImportAutoCmds } from '../wailsjs/go/main/App';
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
const baudOptions = [9600, 19200, 38400, 57600, 115200, 921600];

// Network 参数
const netIp = ref('127.0.0.1');
const netPort = ref('43211');
const udpLocalPort = ref('8081');

// --- TASK：设备选择（数据来自可执行文件同目录的 pwr_dev.ini）---
interface PwrDevice {
  name: string;
  port: string;
  powerOn: string;
  powerOff: string;
}
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
    pwrDevices.value = (list ?? []) as unknown as PwrDevice[];
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
  if (deviceDraft.value.length === 0) {
    deviceDraft.value = [{ name: '', port: '', powerOn: '', powerOff: '' }];
  }
  // 弹窗里的“当前选中”与设备选择框保持一致
  const idx = deviceDraft.value.findIndex(d => d.name === selectedDeviceName.value);
  deviceDraftIndex.value = idx >= 0 ? idx : 0;
  deviceEditorError.value = '';
  showDeviceEditor.value = true;
};

const addDeviceDraft = () => {
  deviceDraft.value.push({ name: '', port: '', powerOn: '', powerOff: '' });
  deviceDraftIndex.value = deviceDraft.value.length - 1;
  deviceEditorError.value = '';
};

const removeDeviceDraft = () => {
  if (deviceDraft.value.length === 0) return;
  deviceDraft.value.splice(deviceDraftIndex.value, 1);
  if (deviceDraft.value.length === 0) {
    deviceDraft.value = [{ name: '', port: '', powerOn: '', powerOff: '' }];
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
    if (!isHexTokenList(row.powerOn)) {
      deviceEditorError.value = `${name} 的 PWR-ON 只能是十六进制数（多个用空格分隔），如 0x01 0x02 AA`;
      return;
    }
    if (!isHexTokenList(row.powerOff)) {
      deviceEditorError.value = `${name} 的 PWR-OFF 只能是十六进制数（多个用空格分隔），如 0xAA 0x00`;
      return;
    }
    list.push({ name, port, powerOn: row.powerOn.trim(), powerOff: row.powerOff.trim() });
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
interface AutoCmdModel { type: AutoCmdType; number: number; text: string; }
const autoCmdDialog = reactive({ show: false, index: -1, text: '', type: 'TX' as AutoCmdType, numberText: '00', numberError: '' });
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
      text: String(c?.text ?? '')
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
  autoCmdDialog.show = true;
  nextTick(() => autoCmdInputRef.value?.focus());
};

// 弹窗里点 TX / RX 标志即切换类型
const toggleDialogCmdType = () => {
  autoCmdDialog.type = autoCmdDialog.type === 'TX' ? 'RX' : 'TX';
};

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
  autoCmds.value.splice(at, 0, { type: 'TX', number: 0, text: '' });
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
  const text = autoCmdDialog.text.replace(/[\r\n]+/g, ' ').trim();
  const number = clampCmdNumber(Number(autoCmdDialog.numberText || '0'));
  if (idx >= 0 && idx < autoCmds.value.length) {
    if (text === '') autoCmds.value.splice(idx, 1);
    else autoCmds.value[idx] = { type: autoCmdDialog.type, number, text };
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
  await pollTaskRuntime();
  startTaskPolling();
};

// 异常暂停后继续
const resumeTask = async () => {
  const res = await ResumeTask();
  if (res) showModal('继续失败', res, 'error');
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
const showDataBitsDropdown = ref(false);
const showParityDropdown = ref(false);
const showStopBitsDropdown = ref(false);
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

// --- 3. UI 状态 (主题 & 弹窗) ---
const showThemePanel = ref(false);
// 定义主题类型以避免索引错误
type ThemeType = {
  bgMain: string;
  bgSide: string;
  primary: string;
  textMain: string;
  textSub: string;
  error: string;
};

const defaultTheme: ThemeType = {
  bgMain: '#F2F1ED',
  bgSide: '#EBEAE6',
  primary: '#7A8B99',
  textMain: '#5C5C5C',
  textSub: '#888888',
  error: '#CF6679'
};
const theme = reactive({ ...defaultTheme });

const cssVars = computed(() => ({
  '--bg-main': theme.bgMain,
  '--bg-side': theme.bgSide,
  '--col-primary': theme.primary,
  '--text-main': theme.textMain,
  '--text-sub': theme.textSub,
  '--col-error': theme.error
}));

const resetTheme = () => Object.assign(theme, defaultTheme);

// 辅助：获取友好的显示名称
const getThemeLabel = (key: string) => {
  const map: Record<string, string> = {
    bgMain: '主背景',
    bgSide: '侧边栏',
    primary: '主色调',
    textMain: '主要文字',
    textSub: '次要文字',
    error: '错误色'
  };
  return map[key] || key;
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

const startProcessingLoop = () => {
    if (animationFrameId !== null) return;
    
    const loop = (timestamp: number) => {
        animationFrameId = requestAnimationFrame(loop);
        
        // 限流，避免过于频繁
        if (timestamp - lastProcessTime < PROCESS_INTERVAL) return;
        lastProcessTime = timestamp;
        
        if (incomingDataQueue.length === 0) return;
        
        // 取出所有待处理数据
        const batchChunks = incomingDataQueue.splice(0, incomingDataQueue.length);
        
        // 计算总长度
        let totalLen = 0;
        for (const chunk of batchChunks) totalLen += chunk.length;
        if (totalLen === 0) return;
        
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
            
            // 3. 更新 receivedData (显示文本)
            if (showHex.value) {
                 receivedData.value += formatData(consolidatedBytes, true);
            } else {
                 // 文本模式按终端语义累加：退格删字符、CR/LF 归一、其余控制字符丢弃
                 receivedData.value = applyTerminalChars(receivedData.value, formatData(consolidatedBytes, false));
            }
            
            // 截断
            if (receivedData.value.length > MAX_BUFFER_SIZE) {
                receivedData.value = receivedData.value.slice(receivedData.value.length - MAX_BUFFER_SIZE);
            }
            
            // 4. 更新计数和滚动
            rxCount.value += totalLen;
            scrollToBottom();
        } catch (e) {
            console.error("Error in processing loop:", e);
        }
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
    const el = e.target as HTMLTextAreaElement | null;
    const hasSelection = !!el && el.selectionStart !== el.selectionEnd;
    if (hasSelection) return;
    e.preventDefault();
    rxLineBuffer.value = '';
    await sendRaw('\x03');
    return;
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

const scrollToBottom = () => {
  if (!autoScroll.value || !logWindowRef.value) return;
  nextTick(() => {
    if(logWindowRef.value) logWindowRef.value.scrollTop = logWindowRef.value.scrollHeight;
  });
};
</script>

<template>
  <div :style="cssVars as any" class="flex h-screen w-screen bg-[var(--bg-main)] text-[var(--text-main)] font-sans overflow-hidden select-none transition-colors duration-300">

    <!-- 侧边栏 -->
    <div class="w-72 bg-[var(--bg-side)] flex flex-col shrink-0 border-r border-black/5 transition-colors duration-300 relative">
      <div class="h-14 flex items-center justify-between px-4 border-b border-black/5">
        <span class="font-bold text-lg tracking-widest text-[var(--col-primary)]">SerialMateX</span>
        <div class="flex items-center gap-2">
          <button @click="showAboutPanel = !showAboutPanel" class="p-1.5 rounded-md hover:bg-black/5 text-[var(--text-sub)] transition-colors" :class="{'bg-black/5': showAboutPanel}" title="关于与更新">
            <svg xmlns="http://www.w3.org/2000/svg" width="18" height="18" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round"><circle cx="12" cy="12" r="10"></circle><line x1="12" y1="16" x2="12" y2="12"></line><line x1="12" y1="8" x2="12.01" y2="8"></line></svg>
          </button>
          <button @click="showThemePanel = !showThemePanel" class="p-1.5 rounded-md hover:bg-black/5 text-[var(--text-sub)] transition-colors" :class="{'bg-black/5': showThemePanel}" title="主题设置">
            <svg xmlns="http://www.w3.org/2000/svg" width="18" height="18" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round"><circle cx="13.5" cy="6.5" r=".5"></circle><circle cx="17.5" cy="10.5" r=".5"></circle><circle cx="8.5" cy="7.5" r=".5"></circle><circle cx="6.5" cy="12.5" r=".5"></circle><path d="M12 2C6.5 2 2 6.5 2 12s4.5 10 10 10c.926 0 1.648-.746 1.648-1.688 0-.437-.18-.835-.437-1.125-.29-.289-.438-.652-.438-1.125a1.64 1.64 0 0 1 1.668-1.668h1.996c3.051 0 5.555-2.503 5.555-5.554C21.965 6.012 17.461 2 12 2z"></path></svg>
          </button>
        </div>
      </div>

      <!-- 主题面板背景遮罩 -->
      <Transition name="backdrop-fade">
        <div v-if="showThemePanel" @click="showThemePanel = false" class="fixed inset-0 bg-black/10 z-10"></div>
      </Transition>

      <!-- 主题面板 (美化版本) -->
      <Transition name="slide-down">
        <div v-if="showThemePanel" @click.stop class="absolute top-14 left-0 w-full bg-white/98 backdrop-blur-xl p-5 shadow-2xl border-b border-black/5 z-20 flex flex-col gap-4 ring-1 ring-black/5">
          <!-- 标题栏 -->
          <div class="flex justify-between items-center">
            <div class="flex items-center gap-2">
              <div class="w-8 h-8 rounded-lg bg-gradient-to-br from-[var(--col-primary)] to-[var(--col-primary)]/70 flex items-center justify-center shadow-sm">
                <svg xmlns="http://www.w3.org/2000/svg" width="16" height="16" viewBox="0 0 24 24" fill="none" stroke="white" stroke-width="2" stroke-linecap="round" stroke-linejoin="round"><circle cx="13.5" cy="6.5" r=".5"></circle><circle cx="17.5" cy="10.5" r=".5"></circle><circle cx="8.5" cy="7.5" r=".5"></circle><circle cx="6.5" cy="12.5" r=".5"></circle><path d="M12 2C6.5 2 2 6.5 2 12s4.5 10 10 10c.926 0 1.648-.746 1.648-1.688 0-.437-.18-.835-.437-1.125-.29-.289-.438-.652-.438-1.125a1.64 1.64 0 0 1 1.668-1.668h1.996c3.051 0 5.555-2.503 5.555-5.554C21.965 6.012 17.461 2 12 2z"></path></svg>
              </div>
              <div>
                <div class="text-sm font-bold text-[var(--text-main)]">主题配色</div>
                <div class="text-[10px] text-[var(--text-sub)]">自定义界面颜色方案</div>
              </div>
            </div>
            <button @click="resetTheme" class="px-3 py-1.5 text-[11px] font-bold text-[var(--col-primary)] hover:bg-[var(--col-primary)]/10 rounded-md transition-all">
              <div class="flex items-center gap-1.5">
                <svg xmlns="http://www.w3.org/2000/svg" width="12" height="12" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round"><path d="M3 12a9 9 0 0 1 9-9 9.75 9.75 0 0 1 6.74 2.74L21 8"></path><path d="M21 3v5h-5"></path><path d="M21 12a9 9 0 0 1-9 9 9.75 9.75 0 0 1-6.74-2.74L3 16"></path><path d="M3 21v-5h5"></path></svg>
                <span>重置</span>
              </div>
            </button>
          </div>


          <div class="grid grid-cols-2 gap-3">
            <div v-for="(val, key) in theme" :key="key" class="group">
              <label class="text-[10px] font-bold text-[var(--text-sub)] uppercase tracking-wider mb-1.5 block">{{ getThemeLabel(key.toString()) }}</label>
              <div class="flex items-center gap-2 bg-gradient-to-r from-black/[0.03] to-black/[0.05] hover:from-black/[0.05] hover:to-black/[0.08] rounded-lg p-2 transition-all border border-black/5 group-hover:border-[var(--col-primary)]/30 group-hover:shadow-sm">
                <input type="color" v-model="theme[key as keyof ThemeType]" class="w-6 h-6 rounded-md cursor-pointer border border-white shadow-sm overflow-hidden shrink-0 p-0">
                <input type="text" v-model="theme[key as keyof ThemeType]" class="flex-1 bg-transparent border-none text-[11px] font-mono text-[var(--text-main)] font-bold focus:outline-none uppercase tracking-wide">
              </div>
            </div>
          </div>

        </div>
      </Transition>

      <!-- 关于面板背景遮罩 -->
      <Transition name="backdrop-fade">
        <div v-if="showAboutPanel" @click="showAboutPanel = false" class="fixed inset-0 bg-black/10 z-10"></div>
      </Transition>

      <!-- 关于与更新面板 (美化版本) -->
      <Transition name="slide-down">
        <div v-if="showAboutPanel" @click.stop class="absolute top-14 left-0 w-full bg-white/98 backdrop-blur-xl p-5 shadow-2xl border-b border-black/5 z-20 flex flex-col gap-4 ring-1 ring-black/5">
          <!-- 标题栏 -->
          <div class="flex items-center gap-3">
            <div class="w-10 h-10 rounded-xl bg-gradient-to-br from-[var(--col-primary)] to-[var(--col-primary)]/70 flex items-center justify-center shadow-lg">
              <svg xmlns="http://www.w3.org/2000/svg" width="20" height="20" viewBox="0 0 24 24" fill="none" stroke="white" stroke-width="2.5" stroke-linecap="round" stroke-linejoin="round"><circle cx="12" cy="12" r="10"></circle><line x1="12" y1="16" x2="12" y2="12"></line><line x1="12" y1="8" x2="12.01" y2="8"></line></svg>
            </div>
            <div class="flex-1">
              <div class="text-sm font-bold text-[var(--text-main)]">关于 SerialMateX</div>
              <div class="text-[10px] text-[var(--text-sub)]">多功能串口通信工具</div>
            </div>
          </div>

          <!-- 版本信息卡片 -->
          <div class="bg-gradient-to-r from-black/[0.03] to-black/[0.05] rounded-lg p-4 border border-black/5">
            <div class="flex items-center justify-between mb-3">
              <div class="flex items-center gap-2">
                <svg xmlns="http://www.w3.org/2000/svg" width="14" height="14" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round" class="text-[var(--col-primary)]"><path d="M21 16V8a2 2 0 0 0-1-1.73l-7-4a2 2 0 0 0-2 0l-7 4A2 2 0 0 0 3 8v8a2 2 0 0 0 1 1.73l7 4a2 2 0 0 0 2 0l7-4A2 2 0 0 0 21 16z"></path><polyline points="3.27 6.96 12 12.01 20.73 6.96"></polyline><line x1="12" y1="22.08" x2="12" y2="12"></line></svg>
                <span class="text-xs font-bold text-[var(--text-sub)]">当前版本</span>
              </div>
              <span class="text-sm font-mono font-bold text-[var(--col-primary)] bg-[var(--col-primary)]/10 px-3 py-1 rounded-full">{{ appVersion }}</span>
            </div>

            <!-- 检查更新按钮 -->
            <button @click="checkForUpdates" 
                    :disabled="updateInfo.checking"
                    class="w-full py-2.5 px-4 rounded-lg text-xs font-bold transition-all disabled:opacity-50 flex items-center justify-center gap-2 shadow-sm"
                    :class="updateInfo.checking ? 'bg-black/5 text-[var(--text-sub)]' : 'bg-[var(--col-primary)] text-white hover:opacity-90 hover:shadow-md'">
              <svg v-if="!updateInfo.checking" xmlns="http://www.w3.org/2000/svg" width="14" height="14" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round"><path d="M21.5 2v6h-6M2.5 22v-6h6M2 11.5a10 10 0 0 1 18.8-4.3M22 12.5a10 10 0 0 1-18.8 4.2"></path></svg>
              <div v-else class="w-3.5 h-3.5 border-2 border-[var(--text-sub)]/30 border-t-[var(--text-sub)] rounded-full animate-spin"></div>
              <span>{{ updateInfo.checking ? '检查中...' : '检查更新' }}</span>
            </button>
          </div>

          <!-- 更新信息卡片 -->
          <div v-if="updateInfo.available" class="bg-gradient-to-r from-[var(--col-primary)]/10 to-[var(--col-primary)]/5 rounded-lg p-4 border border-[var(--col-primary)]/20 shadow-sm">
            <div class="flex items-start gap-2 mb-3">
              <div class="text-2xl">🎉</div>
              <div class="flex-1">
                <div class="font-bold text-[var(--col-primary)] text-sm mb-1">
                  发现新版本
                </div>
                <div class="text-xs text-[var(--text-main)]">
                  <span class="font-mono bg-white/60 px-2 py-0.5 rounded">{{ updateInfo.currentVersion }}</span>
                  <svg xmlns="http://www.w3.org/2000/svg" width="12" height="12" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round" class="inline mx-1 opacity-50"><polyline points="9 18 15 12 9 6"></polyline></svg>
                  <span class="font-mono bg-[var(--col-primary)]/20 px-2 py-0.5 rounded font-bold">{{ updateInfo.latestVersion }}</span>
                </div>
              </div>
            </div>
            
            <!-- 更新说明 -->
            <div v-if="updateInfo.releaseNotes" class="bg-white/60 rounded-lg p-3 mb-3 max-h-28 overflow-y-auto custom-scrollbar border border-black/5">
              <div class="text-[10px] font-bold text-[var(--text-sub)] mb-1.5 uppercase tracking-wider">更新内容</div>
              <div class="text-[11px] text-[var(--text-main)] leading-relaxed whitespace-pre-wrap font-mono">{{ updateInfo.releaseNotes }}</div>
            </div>
            
            <!-- 立即更新按钮 -->
            <button @click="downloadAndInstall" 
                    :disabled="updateProgress.downloading"
                    class="w-full py-2.5 px-4 rounded-lg text-xs font-bold bg-[var(--col-primary)] text-white hover:opacity-90 transition-all disabled:opacity-50 shadow-sm hover:shadow-md flex items-center justify-center gap-2">
              <svg v-if="!updateProgress.downloading" xmlns="http://www.w3.org/2000/svg" width="14" height="14" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round"><path d="M21 15v4a2 2 0 0 1-2 2H5a2 2 0 0 1-2-2v-4"></path><polyline points="7 10 12 15 17 10"></polyline><line x1="12" y1="15" x2="12" y2="3"></line></svg>
              <div v-else class="w-3.5 h-3.5 border-2 border-white/30 border-t-white rounded-full animate-spin"></div>
              <span>{{ updateProgress.downloading ? '下载中...' : '立即更新' }}</span>
            </button>
            
            <!-- 下载进度条 -->
            <div v-if="updateProgress.downloading" class="mt-3">
              <div class="flex justify-between text-[10px] text-[var(--text-sub)] mb-1.5">
                <span class="font-mono">{{ (updateProgress.downloaded / 1024 / 1024).toFixed(2) }} MB / {{ (updateProgress.total / 1024 / 1024).toFixed(2) }} MB</span>
                <span class="font-bold">{{ updateProgress.progress.toFixed(0) }}%</span>
              </div>
              <div class="w-full h-2 bg-white/60 rounded-full overflow-hidden border border-black/5 shadow-inner">
                <div class="h-full bg-gradient-to-r from-[var(--col-primary)] to-[var(--col-primary)]/80 transition-all duration-300 rounded-full" :style="{ width: updateProgress.progress + '%' }"></div>
              </div>
            </div>
          </div>

        </div>
      </Transition>

      <div class="flex-1 overflow-y-auto p-5 space-y-5 custom-scrollbar relative z-10">

        <!-- 功能标签页：SERIAL 与 TASK 并排；NETWORK 收进右侧独立的“三横杠”按钮弹出菜单 -->
        <div class="flex gap-2 transition-transform duration-100" :class="{ 'shake-anim': isShaking }">
          <!-- 常用功能平铺 -->
          <div class="flex-1 bg-white/40 p-1 rounded-lg shadow-sm border border-black/5 flex gap-1 text-[10px] font-bold">
            <button v-for="tab in modeTabs"
                    :key="tab.key"
                    @click="switchMode(tab.key)"
                    class="flex-1 py-1.5 rounded transition-all tracking-wider"
                    :class="mode === tab.key ? 'bg-white text-[var(--col-primary)] shadow-sm' : 'text-[var(--text-sub)] hover:bg-black/5'">
              {{ tab.label }}
            </button>
          </div>

          <!-- 更多功能：独立按钮（三横杠，展开时变为叉），弹出动画沿用原项目的 dropdown-fade -->
          <div class="relative">
            <button @click="showMoreMenu = !showMoreMenu"
                    class="h-full px-2.5 bg-white/40 hover:bg-white/60 rounded-lg shadow-sm border border-black/5 flex items-center justify-center text-[var(--text-sub)] transition-all z-50 relative"
                    :class="{ 'bg-white text-[var(--col-primary)]': showMoreMenu || mode === 'NETWORK' }"
                    title="更多功能：NETWORK">
              <svg class="w-4 h-4 overflow-visible" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round">
                <line x1="3" y1="6" x2="21" y2="6" class="transition-all duration-300 origin-[12px_12px]" :class="showMoreMenu ? 'translate-y-[6px] rotate-45' : ''"></line>
                <line x1="3" y1="12" x2="21" y2="12" class="transition-all duration-300" :class="showMoreMenu ? 'opacity-0' : ''"></line>
                <line x1="3" y1="18" x2="21" y2="18" class="transition-all duration-300 origin-[12px_12px]" :class="showMoreMenu ? '-translate-y-[6px] -rotate-45' : ''"></line>
              </svg>
            </button>

            <div v-if="showMoreMenu" @click="showMoreMenu = false" class="fixed inset-0 z-40 cursor-default"></div>
            <Transition name="dropdown-fade">
              <div v-if="showMoreMenu" class="absolute top-full right-0 mt-2 w-32 bg-white/95 backdrop-blur-xl shadow-xl border border-white/50 rounded-lg p-1.5 z-50 flex flex-col gap-1 ring-1 ring-black/5 origin-top-right">
                <button @click="switchMode('NETWORK')"
                        class="w-full py-1.5 px-2 rounded text-[10px] font-bold tracking-wider transition-all text-center"
                        :class="mode === 'NETWORK' ? 'bg-[var(--col-primary)] text-white shadow-sm' : 'text-[var(--text-main)] hover:bg-black/5'">
                  NETWORK
                </button>
              </div>
            </Transition>
          </div>
        </div>

        <!-- 设置面板主体（任务运行期间只读，避免边跑边改参数） -->
        <div class="bg-white/40 p-3 rounded-lg shadow-sm border border-black/5 space-y-3 overflow-visible transition-opacity"
             :class="{ 'pointer-events-none opacity-60': taskLocked }">
          <div class="text-xs font-bold text-[var(--text-sub)] opacity-70 uppercase tracking-wider mb-1">
            <span>{{ mode === 'TASK' ? 'TASK' : mode }} Settings</span>
          </div>

          <Transition name="fade" mode="out-in">
            <!-- Serial Settings -->
            <div v-if="mode === 'SERIAL'" key="SERIAL" class="space-y-3">
              <!-- 端口 Port -->
              <div class="control-group">
                <label>端口</label>
                <div class="relative flex-1">
                  <button
                      @click="!isConnected && (refreshPorts(), showPortDropdown = !showPortDropdown)"
                      class="w-full morandi-input text-left flex items-center justify-between"
                      :class="{'opacity-60 cursor-not-allowed': isConnected}"
                  >
                    <span>{{ selectedPort || '选择端口' }}</span>
                    <svg class="w-3 h-3 opacity-50 transition-transform duration-200 shrink-0" :class="{'rotate-180': showPortDropdown}" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round"><polyline points="6 9 12 15 18 9"></polyline></svg>
                  </button>
                  <div v-if="showPortDropdown && !isConnected" @click="showPortDropdown = false" class="fixed inset-0 z-0 cursor-default"></div>
                  <Transition name="slide-fade">
                    <div v-if="showPortDropdown && !isConnected" class="absolute top-full left-0 right-0 mt-1 bg-white/95 backdrop-blur-xl shadow-lg border border-white/50 rounded-lg p-1 z-50 flex flex-col max-h-48 overflow-y-auto custom-scrollbar ring-1 ring-black/5">
                      <button v-for="p in portList" :key="p" @click="selectedPort = p; showPortDropdown = false" class="flex items-center justify-between w-full px-3 py-2 text-xs rounded-md transition-all text-left" :class="selectedPort === p ? 'bg-[var(--col-primary)] text-white shadow-sm font-medium' : 'text-[var(--text-main)] hover:bg-black/5'">
                        <span>{{ p }}</span>
                        <span v-if="selectedPort === p" class="text-[10px] font-bold">✓</span>
                      </button>
                      <div v-if="portList.length === 0" class="px-3 py-2 text-xs text-[var(--text-sub)] text-center">无可用端口</div>
                    </div>
                  </Transition>
                </div>
              </div>

              <!-- 波特率 Baud Rate -->
              <div class="control-group">
                <label>波特率</label>
                <div class="relative flex-1">
                  <input type="number" v-model="baudRate" list="baud-list" class="morandi-input" placeholder="Custom" :disabled="isConnected">
                  <datalist id="baud-list">
                    <option v-for="b in baudOptions" :key="b" :value="b"></option>
                  </datalist>
                </div>
              </div>

              <!-- 数据位 Data Bits -->
              <div class="control-group">
                <label>数据位</label>
                <div class="relative flex-1">
                  <button
                      @click="!isConnected && (showDataBitsDropdown = !showDataBitsDropdown)"
                      class="w-full morandi-input text-left flex items-center justify-between"
                      :class="{'opacity-60 cursor-not-allowed': isConnected}"
                  >
                    <span>{{ dataBits }}</span>
                    <svg class="w-3 h-3 opacity-50 transition-transform duration-200 shrink-0" :class="{'rotate-180': showDataBitsDropdown}" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round"><polyline points="6 9 12 15 18 9"></polyline></svg>
                  </button>
                  <div v-if="showDataBitsDropdown && !isConnected" @click="showDataBitsDropdown = false" class="fixed inset-0 z-0 cursor-default"></div>
                  <Transition name="slide-fade">
                    <div v-if="showDataBitsDropdown && !isConnected" class="absolute top-full left-0 right-0 mt-1 bg-white/95 backdrop-blur-xl shadow-lg border border-white/50 rounded-lg p-1 z-50 flex flex-col ring-1 ring-black/5">
                      <button v-for="opt in dataBitsOptions" :key="opt" @click="dataBits = opt; showDataBitsDropdown = false" class="flex items-center justify-between w-full px-3 py-2 text-xs rounded-md transition-all text-left" :class="dataBits === opt ? 'bg-[var(--col-primary)] text-white shadow-sm font-medium' : 'text-[var(--text-main)] hover:bg-black/5'">
                        <span>{{ opt }}</span>
                        <span v-if="dataBits === opt" class="text-[10px] font-bold">✓</span>
                      </button>
                    </div>
                  </Transition>
                </div>
              </div>

              <!-- 停止位 Stop Bits（与校验位互换位置） -->
              <div class="control-group">
                <label>停止位</label>
                <div class="relative flex-1">
                  <button
                      @click="!isConnected && (showStopBitsDropdown = !showStopBitsDropdown)"
                      class="w-full morandi-input text-left flex items-center justify-between"
                      :class="{'opacity-60 cursor-not-allowed': isConnected}"
                  >
                    <span>{{ stopBits }}</span>
                    <svg class="w-3 h-3 opacity-50 transition-transform duration-200 shrink-0" :class="{'rotate-180': showStopBitsDropdown}" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round"><polyline points="6 9 12 15 18 9"></polyline></svg>
                  </button>
                  <div v-if="showStopBitsDropdown && !isConnected" @click="showStopBitsDropdown = false" class="fixed inset-0 z-0 cursor-default"></div>
                  <Transition name="slide-fade">
                    <div v-if="showStopBitsDropdown && !isConnected" class="absolute top-full left-0 right-0 mt-1 bg-white/95 backdrop-blur-xl shadow-lg border border-white/50 rounded-lg p-1 z-50 flex flex-col ring-1 ring-black/5">
                      <button v-for="opt in stopBitsOptions" :key="opt" @click="stopBits = opt; showStopBitsDropdown = false" class="flex items-center justify-between w-full px-3 py-2 text-xs rounded-md transition-all text-left" :class="stopBits === opt ? 'bg-[var(--col-primary)] text-white shadow-sm font-medium' : 'text-[var(--text-main)] hover:bg-black/5'">
                        <span>{{ opt }}</span>
                        <span v-if="stopBits === opt" class="text-[10px] font-bold">✓</span>
                      </button>
                    </div>
                  </Transition>
                </div>
              </div>

              <!-- 校验位 Parity（与停止位互换位置） -->
              <div class="control-group">
                <label>校验位</label>
                <div class="relative flex-1">
                  <button
                      @click="!isConnected && (showParityDropdown = !showParityDropdown)"
                      class="w-full morandi-input text-left flex items-center justify-between"
                      :class="{'opacity-60 cursor-not-allowed': isConnected}"
                  >
                    <span>{{ parity }}</span>
                    <svg class="w-3 h-3 opacity-50 transition-transform duration-200 shrink-0" :class="{'rotate-180': showParityDropdown}" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round"><polyline points="6 9 12 15 18 9"></polyline></svg>
                  </button>
                  <div v-if="showParityDropdown && !isConnected" @click="showParityDropdown = false" class="fixed inset-0 z-0 cursor-default"></div>
                  <Transition name="slide-fade">
                    <div v-if="showParityDropdown && !isConnected" class="absolute top-full left-0 right-0 mt-1 bg-white/95 backdrop-blur-xl shadow-lg border border-white/50 rounded-lg p-1 z-50 flex flex-col ring-1 ring-black/5">
                      <button v-for="opt in parityOptions" :key="opt.value" @click="parity = opt.value; showParityDropdown = false" class="flex items-center justify-between w-full px-3 py-2 text-xs rounded-md transition-all text-left" :class="parity === opt.value ? 'bg-[var(--col-primary)] text-white shadow-sm font-medium' : 'text-[var(--text-main)] hover:bg-black/5'">
                        <span>{{ opt.label }}</span>
                        <span v-if="parity === opt.value" class="text-[10px] font-bold">✓</span>
                      </button>
                    </div>
                  </Transition>
                </div>
              </div>

              <!-- 显示选项：Hex 显示 / 自动滚屏（由左侧栏底部的 DISPLAY 移入本页） -->
              <div class="pt-2 mt-1 border-t border-black/5 space-y-2">
                <div class="text-[10px] font-bold text-[var(--text-sub)] opacity-70 uppercase tracking-wider">Display</div>
                <label class="flex items-center space-x-2 cursor-pointer hover:text-[var(--col-primary)] transition-colors">
                  <input type="checkbox" v-model="showHex" class="accent-[var(--col-primary)] w-4 h-4">
                  <span class="text-xs">Hex 显示</span>
                </label>
                <label class="flex items-center space-x-2 cursor-pointer hover:text-[var(--col-primary)] transition-colors">
                  <input type="checkbox" v-model="autoScroll" class="accent-[var(--col-primary)] w-4 h-4">
                  <span class="text-xs">自动滚屏</span>
                </label>
              </div>
            </div>

            <!-- Network Settings：TCP 客户端 / TCP 服务端 / UDP 合并为一个标签页 -->
            <div v-else-if="mode === 'NETWORK'" key="NETWORK" class="space-y-3">
              <!-- 子模式切换 -->
              <div class="grid grid-cols-3 gap-1 p-1 bg-black/[0.03] rounded-md text-[9px] font-bold tracking-wide">
                <button v-for="opt in netModeTabs"
                        :key="opt.key"
                        @click="switchNetMode(opt.key)"
                        class="py-1.5 rounded transition-all"
                        :class="netMode === opt.key ? 'bg-white text-[var(--col-primary)] shadow-sm' : 'text-[var(--text-sub)] hover:bg-black/5'">
                  {{ opt.label }}
                </button>
              </div>

              <template v-if="netMode === 'TCP_CLIENT'">
                <div class="control-group"><label>IP</label><input type="text" v-model="netIp" class="morandi-input" placeholder="127.0.0.1" :disabled="isConnected"></div>
                <div class="control-group"><label>Port</label><input type="text" v-model="netPort" class="morandi-input" placeholder="43211" :disabled="isConnected"></div>
              </template>

              <template v-else-if="netMode === 'TCP_SERVER'">
                <div class="control-group"><label>Local Port</label><input type="text" v-model="netPort" class="morandi-input" placeholder="8080" :disabled="isConnected"></div>
              </template>

              <template v-else>
                <div class="control-group"><label>Local Port</label><input type="text" v-model="udpLocalPort" class="morandi-input" placeholder="8081" :disabled="isConnected"></div>
                <div class="my-2 border-t border-black/5"></div>
                <div class="control-group"><label>Target IP</label><input type="text" v-model="netIp" class="morandi-input" placeholder="127.0.0.1" :disabled="isConnected"></div>
                <div class="control-group"><label>Target Port</label><input type="text" v-model="netPort" class="morandi-input" placeholder="8080" :disabled="isConnected"></div>
              </template>
            </div>

            <!-- TASK：设备选择（读取 exe 同目录的 pwr_dev.ini） -->
            <div v-else-if="mode === 'TASK'" key="TASK" class="space-y-3">
              <div class="control-group">
                <label>设备选择</label>
                <div class="relative flex-1">
                  <button
                      @click="!isConnected && (showDeviceDropdown = !showDeviceDropdown)"
                      class="w-full h-8 morandi-input text-left flex items-center justify-between"
                      :class="{'opacity-60 cursor-not-allowed': isConnected}"
                  >
                    <span class="truncate">{{ selectedDeviceName || '未找到设备' }}</span>
                    <svg class="w-3 h-3 opacity-50 transition-transform duration-200 shrink-0" :class="{'rotate-180': showDeviceDropdown}" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round"><polyline points="6 9 12 15 18 9"></polyline></svg>
                  </button>
                  <div v-if="showDeviceDropdown && !isConnected" @click="showDeviceDropdown = false" class="fixed inset-0 z-0 cursor-default"></div>
                  <Transition name="slide-fade">
                    <div v-if="showDeviceDropdown && !isConnected" class="absolute top-full left-0 right-0 mt-1 bg-white/95 backdrop-blur-xl shadow-lg border border-white/50 rounded-lg p-1 z-50 flex flex-col max-h-48 overflow-y-auto custom-scrollbar ring-1 ring-black/5">
                      <button v-for="dev in pwrDevices" :key="dev.name" @click="selectedDeviceName = dev.name; showDeviceDropdown = false" class="flex items-center justify-between w-full px-3 py-2 text-xs rounded-md transition-all text-left" :class="selectedDeviceName === dev.name ? 'bg-[var(--col-primary)] text-white shadow-sm font-medium' : 'text-[var(--text-main)] hover:bg-black/5'">
                        <span class="truncate">{{ dev.name }}</span>
                        <span v-if="selectedDeviceName === dev.name" class="text-[10px] font-bold">✓</span>
                      </button>
                      <div v-if="pwrDevices.length === 0" class="px-3 py-2 text-xs text-[var(--text-sub)] text-center">pwr_dev.ini 中没有设备</div>
                    </div>
                  </Transition>
                </div>
              </div>

              <!-- 选中设备的 4 项信息不再常驻显示，改为“编辑设备参数”按钮（高度与设备选择框一致） -->
              <button @click="openDeviceEditor"
                      class="w-full h-8 px-2 rounded-md bg-white/60 hover:bg-white border border-black/10 text-[11px] font-bold text-[var(--text-main)] transition-all flex items-center justify-center gap-1.5 active:scale-[0.99]">
                <svg class="w-3.5 h-3.5 opacity-70" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round"><path d="M12 20h9"></path><path d="M16.5 3.5a2.12 2.12 0 0 1 3 3L7 19l-4 1 1-4Z"></path></svg>
                编辑设备参数
              </button>

              <!-- 保存日志 / 保存功耗 / 初始状态：右侧开关与数值框同尺寸同配色，点一下切换开或关 -->
              <div class="pt-2 mt-1 border-t border-black/5 space-y-2">
                <div class="flex items-center justify-between gap-2">
                  <span class="text-[12px] text-[var(--text-sub)]">保存日志</span>
                  <button @click="toggleTaskFlag('saveLog')"
                          class="w-[64px] px-1.5 py-1 rounded-md border border-black/10 font-mono text-[12px] transition-all text-center"
                          :class="taskConfig.saveLog ? 'bg-white text-[var(--col-primary)] font-bold' : 'bg-white/60 text-[var(--text-sub)] hover:bg-white'"
                          :title="taskConfig.saveLog ? '点击改为关' : '点击改为开'">
                    {{ taskConfig.saveLog ? '开' : '关' }}
                  </button>
                </div>
                <div class="flex items-center justify-between gap-2">
                  <span class="text-[12px] text-[var(--text-sub)]">保存功耗</span>
                  <button disabled
                          class="w-[64px] px-1.5 py-1 rounded-md border border-black/10 font-mono text-[12px] text-center bg-white/40 text-[var(--text-sub)] cursor-not-allowed"
                          title="当前版本不支持：固定为关">
                    关
                  </button>
                </div>
                <div class="flex items-center justify-between gap-2">
                  <span class="text-[12px] text-[var(--text-sub)]">初始状态</span>
                  <button @click="toggleTaskFlag('initialState')"
                          class="w-[64px] px-1.5 py-1 rounded-md border border-black/10 font-mono text-[12px] transition-all text-center"
                          :class="taskConfig.initialState ? 'bg-white text-[var(--col-primary)] font-bold' : 'bg-white/60 text-[var(--text-sub)] hover:bg-white'"
                          :title="taskConfig.initialState ? '点击改为关' : '点击改为开'">
                    {{ taskConfig.initialState ? '开' : '关' }}
                  </button>
                </div>
                <div class="flex items-center justify-between gap-2">
                  <span class="text-[12px] text-[var(--text-sub)]">异常暂停</span>
                  <button @click="toggleTaskFlag('abnormalPause')"
                          class="w-[64px] px-1.5 py-1 rounded-md border border-black/10 font-mono text-[12px] transition-all text-center"
                          :class="taskConfig.abnormalPause ? 'bg-white text-[var(--col-primary)] font-bold' : 'bg-white/60 text-[var(--text-sub)] hover:bg-white'"
                          :title="taskConfig.abnormalPause ? '点击改为关' : '点击改为开'">
                    {{ taskConfig.abnormalPause ? '开' : '关' }}
                  </button>
                </div>
              </div>

              <!-- 运行时间 / 间隔时间 / 运行次数：点数字弹窗编辑，保存即写回 task_config.ini -->
              <div class="pt-2 mt-1 border-t border-black/5 space-y-2">
                <div class="flex items-center justify-between gap-2">
                  <span class="text-[12px] text-[var(--text-sub)]">运行时间</span>
                  <button @click="openNumberDialog('waitSeconds')"
                          class="w-[64px] px-1.5 py-1 rounded-md bg-white/60 hover:bg-white border border-black/10 font-mono text-[12px] text-[var(--text-main)] transition-all flex items-baseline justify-end gap-1"
                          title="点击编辑运行时间">
                    <span class="tabular-nums">{{ taskConfig.waitSeconds }}</span>
                    <span class="text-[var(--text-sub)]">秒</span>
                  </button>
                </div>
                <div class="flex items-center justify-between gap-2">
                  <span class="text-[12px] text-[var(--text-sub)]">间隔时间</span>
                  <button @click="openNumberDialog('delaySeconds')"
                          class="w-[64px] px-1.5 py-1 rounded-md bg-white/60 hover:bg-white border border-black/10 font-mono text-[12px] text-[var(--text-main)] transition-all flex items-baseline justify-end gap-1"
                          title="点击编辑间隔时间">
                    <span class="tabular-nums">{{ taskConfig.delaySeconds }}</span>
                    <span class="text-[var(--text-sub)]">秒</span>
                  </button>
                </div>
                <div class="flex items-center justify-between gap-2">
                  <span class="text-[12px] text-[var(--text-sub)]">运行次数</span>
                  <button @click="openNumberDialog('runCount')"
                          class="w-[64px] px-1.5 py-1 rounded-md bg-white/60 hover:bg-white border border-black/10 font-mono text-[12px] text-[var(--text-main)] transition-all flex items-baseline justify-end gap-1"
                          title="点击编辑运行次数">
                    <span class="tabular-nums">{{ taskConfig.runCount }}</span>
                    <span class="text-[var(--text-sub)]">次</span>
                  </button>
                </div>
              </div>
            </div>
          </Transition>
        </div>

        <!-- TASK STATUS：只在 TASK 页显示，位于 TASK SETTINGS 下方 -->
        <div v-if="mode === 'TASK'" class="bg-white/40 p-3 rounded-lg shadow-sm border border-black/5 space-y-3">
          <div class="text-xs font-bold text-[var(--text-sub)] opacity-70 uppercase tracking-wider mb-1">TASK STATUS</div>
          <div class="flex items-center justify-between gap-2">
            <span class="text-[12px] text-[var(--text-sub)]">当前轮次</span>
            <span class="font-mono text-[12px] tabular-nums text-[var(--text-main)]">
              {{ taskStatus.round }}次/<span class="text-[var(--col-primary)] font-bold">{{ taskConfig.runCount }}</span>次
            </span>
          </div>
          <div class="flex items-center justify-between gap-2">
            <span class="text-[12px] text-[var(--text-sub)]">正常次数</span>
            <span class="font-mono text-[12px] tabular-nums text-[var(--text-main)]">
              <span class="text-[#3f7d5b] font-bold">{{ taskRt.normalCount }}</span>次
            </span>
          </div>
          <div class="flex items-center justify-between gap-2">
            <span class="text-[12px] text-[var(--text-sub)]">电源状态</span>
            <span class="w-[64px] px-1.5 py-1 rounded-md border font-mono text-[12px] text-center transition-all"
                  :class="taskStatus.power === 'ON'
                    ? 'bg-[#3f7d5b]/[0.10] border-[#3f7d5b]/30 text-[#3f7d5b] font-bold'
                    : 'bg-white/60 border-black/10 text-[var(--text-sub)]'">{{ taskStatus.power }}</span>
          </div>
          <!-- 实时倒计时：N 秒后上电 / N 秒后掉电 -->
          <div class="flex items-center justify-between gap-2">
            <span class="text-[12px] text-[var(--text-sub)]">下一动作</span>
            <span class="font-mono text-[12px] tabular-nums text-[var(--col-primary)]">
              <template v-if="taskRt.running && taskRt.countdown > 0 && taskRt.countdownTo">
                {{ taskRt.countdown }} 秒后{{ taskRt.countdownTo === 'powerOn' ? '上电' : '掉电' }}
              </template>
              <template v-else-if="taskRt.paused">已暂停</template>
              <template v-else>—</template>
            </span>
          </div>
          <div class="flex items-center justify-between gap-2" title="当前版本不支持电流采集：固定显示 NULL">
            <span class="text-[12px] text-[var(--text-sub)]">当前电流</span>
            <span class="w-[64px] px-1.5 py-1 rounded-md bg-white/40 border border-black/10 font-mono text-[12px] text-center tabular-nums text-[var(--text-sub)] cursor-not-allowed">
              NULL<span class="ml-0.5 text-[10px] text-[var(--text-sub)]">A</span>
            </span>
          </div>
          <!-- 异常暂停时给一个“继续”入口 -->
          <button v-if="taskRt.paused"
                  @click="resumeTask"
                  class="w-full h-7 rounded-md bg-[#b3564e] hover:bg-[#c0655c] text-white text-[11px] font-bold transition-all">
            继续（解除异常暂停）
          </button>
        </div>

        <!-- 连接按钮只在 SERIAL / NETWORK 页出现（TASK 页是纯配置页，不显示该按钮） -->
        <button v-if="mode !== 'TASK'"
                @click="toggleConnection"
                :disabled="taskLocked"
                class="w-full py-2.5 rounded-lg font-medium text-white transition-all duration-300 transform active:scale-[0.98] shadow-sm flex items-center justify-center space-x-2 bg-[var(--col-primary)] hover:opacity-90"
                :class="{ 'opacity-50 cursor-not-allowed pointer-events-none': taskLocked }"
                :title="taskLocked ? '任务运行中，先停止任务再断开连接' : ''">
          <div v-if="!isConnected" class="w-2 h-2 rounded-full bg-white animate-pulse"></div>
          <span>{{ isConnected ? '断开' : '连接' }}</span>
        </button>
      </div>
    </div>

    <!-- 中间主区域 (RX / TX)；宽度最小为左侧栏的 2 倍（288 × 2 = 576px） -->
    <div class="flex-1 flex flex-col min-w-[576px] min-h-0 p-4 transition-colors duration-300">
      <div class="flex-1 min-h-0 bg-white/60 rounded-xl shadow-[0_2px_12px_-4px_rgba(0,0,0,0.08)] border border-black/5 flex flex-col overflow-hidden relative backdrop-blur-sm">
        <div class="h-10 px-4 flex items-center justify-between bg-black/[0.02] border-b border-black/5">
          <div class="flex items-center space-x-4">
            <div class="flex items-center space-x-2">
              <span class="text-xs font-bold text-[var(--col-primary)] tracking-wider">RX MONITOR</span>
              <span class="text-[10px] text-[var(--text-sub)] bg-black/5 px-1.5 py-0.5 rounded-md">{{ rxCount }} Bytes</span>
              <span v-if="isConnected" class="text-[10px] text-[var(--text-sub)]/70 select-none hidden sm:inline">点击接收区即可直接键入发送</span>
            </div>
            
            <!-- View Mode Switch -->
            <div class="bg-black/5 p-0.5 rounded-md flex">
               <button @click="showChart = false" 
                       class="px-2 py-0.5 text-[10px] font-bold rounded flex items-center gap-1 transition-all"
                       :class="!showChart ? 'bg-white text-[var(--col-primary)] shadow-sm' : 'text-[var(--text-sub)] hover:text-[var(--text-main)]'">
                 <span>Text</span>
               </button>
               <button @click="showChart = true" 
                       class="px-2 py-0.5 text-[10px] font-bold rounded flex items-center gap-1 transition-all"
                       :class="showChart ? 'bg-white text-[var(--col-primary)] shadow-sm' : 'text-[var(--text-sub)] hover:text-[var(--text-main)]'">
                 <span>Plot</span>
               </button>
            </div>
          </div>
          <button @click="clearReceive" title="清空" class="group flex items-center justify-center w-7 h-7 rounded hover:bg-white hover:shadow-sm text-[var(--text-sub)] hover:text-[var(--col-primary)] transition-all">
            <svg class="w-4 h-4 broom-icon" :class="{ 'broom-clicked': isBroomClicked }" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round"><path d="M18 6L7.5 16.5"></path><path d="M19.5 4.5L16.5 7.5"></path><path d="M2 22L4.5 19.5"></path><path d="M9.5 12.5C7.5 14.5 6 15 5 16C4 17 3 17 3 17C3 18 4 19C5 20 5 20 5 20C5 20 6 20 7 19C8 18 8.5 16.5 10.5 14.5L18 7"></path></svg>
          </button>
        </div>
        <div v-if="showChart" class="flex-1 w-full bg-transparent p-2 relative">
             <v-chart class="chart" :option="chartOption" autoresize />
        </div>
        <textarea v-else
                  ref="logWindowRef"
                  readonly
                  class="flex-1 w-full p-4 font-mono text-sm bg-transparent resize-none outline-none custom-scrollbar leading-relaxed text-[var(--text-main)] cursor-text"
                  :value="receivedData"
                  placeholder="点击这里后直接键入即可发送（Enter 发单个回车、↑/↓ 翻历史、Backspace 发 0x7F、Ctrl+C 中断；选中文本可 Ctrl+C 复制，Ctrl+V 直接发给下位机）"
                  @keydown="onRxKeydown"
                  @paste="onRxPaste"></textarea>
      </div>

      <!-- TX EDITOR（可折叠；展开/收起按钮固定在整列最下方） -->
      <div class="shrink-0 transition-all duration-300 ease-out"
           :class="showTxEditor ? 'h-[76px] mt-2 opacity-100 overflow-visible' : 'h-0 mt-0 opacity-0 overflow-hidden'">
        <div class="h-full bg-white/60 rounded-xl shadow-[0_2px_12px_-4px_rgba(0,0,0,0.08)] border border-black/5 flex flex-col overflow-visible backdrop-blur-sm">
        <div class="h-9 px-4 flex items-center justify-between bg-black/[0.02] border-b border-black/5 rounded-t-xl">
          <div class="flex items-center space-x-4">
            <span class="text-xs font-bold text-[var(--text-sub)] tracking-wider">TX EDITOR</span>
            <div class="flex items-center gap-3">
              <label class="flex items-center space-x-1.5 cursor-pointer hover:text-[var(--col-primary)] transition-colors select-none">
                <input type="checkbox" v-model="hexSend" class="accent-[var(--col-primary)] w-3.5 h-3.5 rounded-sm">
                <span class="text-[11px] font-bold opacity-70">Hex Send</span>
              </label>
              <div class="w-[1px] h-3 bg-black/10"></div>
              <div class="relative z-10" :class="{'opacity-50 pointer-events-none': hexSend}">
                <button
                    @click="showEolDropdown = !showEolDropdown"
                    class="flex items-center space-x-1.5 bg-black/5 hover:bg-black/10 transition-all px-2.5 rounded-md border border-transparent focus:border-black/5 outline-none h-7"
                    :class="{'text-[var(--col-primary)] bg-[var(--col-primary)]/10 border-[var(--col-primary)]/20': showEolDropdown}"
                >
                  <div class="flex items-baseline space-x-1 translate-y-[0.5px]">
                    <span class="text-[11px] font-bold opacity-70 leading-tight">EOL:</span>
                    <span class="text-[11px] font-mono font-medium min-w-[30px] text-center leading-tight">{{ currentEolLabel }}</span>
                  </div>
                  <svg class="w-3 h-3 opacity-50 transform transition-transform duration-200" :class="{'rotate-180': showEolDropdown}" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round"><polyline points="6 9 12 15 18 9"></polyline></svg>
                </button>
                <div v-if="showEolDropdown" @click="showEolDropdown = false" class="fixed inset-0 z-0 cursor-default"></div>
                <Transition name="slide-fade">
                  <div v-if="showEolDropdown" class="absolute bottom-full right-0 mb-1.5 w-32 bg-white/80 backdrop-blur-xl shadow-[0_4px_16px_-4px_rgba(0,0,0,0.1)] border border-white/50 rounded-lg p-1 z-50 flex flex-col overflow-hidden select-none ring-1 ring-black/5">
                    <button v-for="opt in eolOptions" :key="opt.value" @click="selectEol(opt.value as any)" class="relative flex items-center justify-between w-full px-3 py-2 text-[11px] font-mono rounded-md transition-all outline-none" :class="lineEndingMode === opt.value ? 'bg-[var(--col-primary)] text-white shadow-sm font-medium' : 'text-[var(--text-main)] hover:bg-black/5'">
                      <span>{{ opt.label }}</span>
                      <span v-if="lineEndingMode === opt.value" class="text-[10px] font-bold">✓</span>
                    </button>
                  </div>
                </Transition>
              </div>
            </div>
          </div>
        </div>

        <!-- 单行输入：编辑后回车发送，↑ / ↓ 翻历史 -->
        <div class="flex-1 flex items-center px-3 pb-2">
          <input ref="txInputRef"
                 v-model="sendInput"
                 type="text"
                 spellcheck="false"
                 autocomplete="off"
                 class="w-full h-8 bg-white/50 border border-transparent focus:border-[var(--col-primary)]/30 rounded-lg px-3 font-mono text-sm text-[var(--text-main)] focus:bg-white transition-all outline-none placeholder-[var(--text-sub)]/50"
                 :placeholder="hexSend ? '输入 Hex（如 AA BB CC）后回车发送' : '输入内容后回车发送，↑ / ↓ 翻历史'"
                 @keydown.enter.prevent="handleSend"
                 @keydown.up.prevent="txHistoryPrev"
                 @keydown.down.prevent="txHistoryNext" />
        </div>
        </div>
      </div>

      <!-- 细长按钮：展开 / 收起 TX EDITOR（始终固定在整列底部，宽度与 RX MONITOR 一致） -->
      <button @click="showTxEditor = !showTxEditor"
              class="mt-2 h-5 w-full shrink-0 flex items-center justify-center gap-1.5 rounded-md bg-white/50 hover:bg-white/80 border border-black/5 text-[9px] font-bold tracking-[0.2em] text-[var(--text-sub)] hover:text-[var(--col-primary)] transition-all select-none"
              :title="showTxEditor ? '收起 TX EDITOR' : '展开 TX EDITOR'">
        <svg class="w-3 h-3 transition-transform duration-300" :class="{'rotate-180': !showTxEditor}" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="3" stroke-linecap="round" stroke-linejoin="round"><polyline points="6 15 12 9 18 15"></polyline></svg>
        <span>TX EDITOR</span>
      </button>
    </div>

    <!-- 右侧栏：AUTO CMD（宽度与左侧栏一致 288px，不随窗口缩放） -->
    <aside class="w-72 shrink-0 bg-[var(--bg-side)] border-l border-black/5 transition-colors duration-300 flex flex-col">
      <!-- 标题 + 新增 / 删除 / 导入 -->
      <div class="px-3 pt-4 pb-2 flex items-center justify-between shrink-0">
        <span class="text-[10px] font-bold tracking-[0.2em] text-[var(--text-sub)]">AUTO CMD</span>
        <div class="flex items-center gap-1">
          <button @click="addAutoCmd"
                  :disabled="taskLocked"
                  class="px-2 h-6 rounded-md bg-white/60 hover:bg-white border border-black/10 text-[11px] font-medium text-[var(--text-main)] transition-all active:scale-95 disabled:opacity-40 disabled:cursor-not-allowed disabled:active:scale-100"
                  title="新增一条命令（有选中时插入到该条上方）">新增</button>
          <button @click="removeAutoCmd"
                  :disabled="autoCmdSelected < 0 || taskLocked"
                  class="px-2 h-6 rounded-md bg-white/60 hover:bg-white border border-black/10 text-[11px] font-medium text-[var(--text-main)] transition-all active:scale-95 disabled:opacity-40 disabled:cursor-not-allowed disabled:active:scale-100"
                  title="删除选中的命令">删除</button>
          <button @click="importAutoCmds"
                  :disabled="taskLocked"
                  class="px-2 h-6 rounded-md bg-white/60 hover:bg-white border border-black/10 text-[11px] font-medium text-[var(--text-main)] transition-all active:scale-95 disabled:opacity-40 disabled:cursor-not-allowed disabled:active:scale-100"
                  title="从 txt 文件导入命令（覆盖当前列表）">导入</button>
        </div>
      </div>

      <!-- 命令列表：序号 + 文本，单击选中，双击编辑，点空白处取消选中；运行期间只读且高亮当前命令 -->
      <div class="flex-1 min-h-0 overflow-y-auto custom-scrollbar px-2 py-1 space-y-1"
           :class="{ 'pointer-events-none opacity-70': taskLocked }"
           @click.self="autoCmdSelected = -1">
        <button v-for="(cmd, i) in autoCmds" :key="i"
                @click="autoCmdSelected = i"
                @dblclick="openAutoCmdDialog(i)"
                :title="`${cmd.type} ${cmd.text}`"
                class="w-full flex items-start gap-2 px-2 py-1.5 rounded-md border text-left transition-all"
                :class="[
                  taskRt.running && taskRt.cmdIndex === i
                    ? (taskRt.cmdPhase === 'wait' ? 'bg-[#3f7d5b]/10 border-[#3f7d5b]/40 shadow-sm ring-1 ring-[#3f7d5b]/30' : 'bg-white border-[var(--col-primary)] shadow-sm ring-1 ring-[var(--col-primary)]/30')
                    : (autoCmdSelected === i ? 'bg-white border-[var(--col-primary)]/40 shadow-sm' : 'bg-white/40 border-transparent hover:bg-white/80')
                ]">
          <span class="w-5 h-5 shrink-0 mt-px flex items-center justify-center rounded bg-black/[0.07] text-[10px] font-mono font-bold tabular-nums text-[var(--col-primary)] leading-none">{{ i + 1 }}</span>
          <!-- TX / RX 标志：TX 绿色、RX 红色，与序号、命令文本都能区分开 -->
          <span class="w-[18px] shrink-0 mt-px text-[9px] font-bold leading-5 tracking-wider"
                :class="cmd.type === 'RX' ? 'text-[#b3564e]' : 'text-[#3f7d5b]'">{{ cmd.type }}</span>
          <span class="flex-1 min-w-0 pl-2 border-l border-black/10 text-[11px] font-mono leading-5 text-[var(--text-main)] break-all">{{ cmd.text }}</span>
          <!-- 行尾数字：固定两位（00 ~ 99）；运行到该条时用倒计时覆盖显示 -->
          <span class="w-7 shrink-0 mt-px h-5 flex items-center justify-center rounded font-mono text-[10px] tabular-nums select-none"
                :class="taskRt.running && taskRt.cmdIndex === i
                  ? (taskRt.cmdPhase === 'wait' ? 'bg-[#3f7d5b] text-white font-bold' : 'bg-black/[0.05] text-[var(--text-sub)]')
                  : 'bg-black/[0.05] text-[var(--text-sub)]'"
                :title="taskRt.running && taskRt.cmdIndex === i ? '间隔倒计时' : '双击命令可修改该数字'">
            {{ taskRt.running && taskRt.cmdIndex === i && taskRt.cmdPhase === 'wait'
              ? String(taskRt.cmdCountdown)
              : formatCmdNumber(cmd.number) }}
          </span>
        </button>
        <div v-if="autoCmds.length === 0" @click="autoCmdSelected = -1"
             class="px-3 py-8 text-center text-[11px] leading-5 text-[var(--text-sub)]">
          点击「新增」添加命令<br />双击可编辑
        </div>
      </div>

      <!-- 底部：启动 / 停止（运行中沿边框均匀顺时针流光，停止时外圈呼吸灯） -->
      <div class="p-3 shrink-0">
        <div class="run-ring" :class="taskRt.running ? 'run-ring--running' : 'run-ring--idle'">
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
            <rect x="1.5" y="1.5" width="97" height="97" rx="5" ry="5" fill="none"
                  stroke="rgba(255,255,255,0.30)" stroke-width="2.5" vector-effect="non-scaling-stroke" />
            <!-- 高光段：沿整圈匀速流动 -->
            <rect x="1.5" y="1.5" width="97" height="97" rx="5" ry="5" fill="none"
                  stroke="url(#runRingGrad)" stroke-width="2.5" stroke-linecap="round"
                  pathLength="100" stroke-dasharray="20 80" vector-effect="non-scaling-stroke"
                  class="run-ring__dash" />
          </svg>
          <button @click="toggleAutoRun"
                  :disabled="!taskRt.running && !canStartTask"
                  class="relative z-10 w-full h-11 rounded-[10px] text-[13px] font-bold tracking-[0.2em] text-white transition-colors duration-300 shadow-sm"
                  :class="[
                    taskRt.running ? 'bg-[#9c7168] hover:bg-[#aa7c72]' : 'bg-[#55656d] hover:bg-[#616f77]',
                    !taskRt.running && !canStartTask ? 'opacity-45 cursor-not-allowed' : ''
                  ]"
                  :title="!taskRt.running && !canStartTask ? '请先在 SERIAL 页连接 COM 口' : (taskRt.running ? '停止任务' : '开始任务')">
            {{ taskRt.running ? '停止' : '启动' }}
          </button>
        </div>
      </div>
    </aside>

    <!-- TASK：设备参数编辑弹窗（新增 / 删除 / 保存 / 取消） -->
    <Transition name="modal-fade">
      <div v-if="showDeviceEditor" class="fixed inset-0 z-50 flex items-center justify-center bg-black/20 backdrop-blur-[2px] transition-all">
        <div class="bg-white/95 rounded-xl shadow-2xl border border-white/50 w-[560px] max-w-[92%] max-h-[86vh] overflow-hidden transform transition-all flex flex-col" @click.stop>
          <div class="h-10 shrink-0 flex items-center justify-between px-4 bg-black/[0.03] border-b border-black/5">
            <div class="flex items-center gap-2">
              <svg class="w-4 h-4 text-[var(--col-primary)]" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round"><path d="M12 20h9"></path><path d="M16.5 3.5a2.12 2.12 0 0 1 3 3L7 19l-4 1 1-4Z"></path></svg>
              <span class="text-xs font-bold tracking-wide text-[var(--col-primary)]">编辑设备参数</span>
            </div>
            <button @click="cancelDeviceEditor" class="text-[var(--text-sub)] hover:text-[var(--text-main)] transition-colors"><svg class="w-4 h-4" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round"><line x1="18" y1="6" x2="6" y2="18"></line><line x1="6" y1="6" x2="18" y2="18"></line></svg></button>
          </div>

          <div class="p-3 overflow-y-auto custom-scrollbar space-y-2 flex-1">
            <div v-for="(row, idx) in deviceDraft"
                 :key="idx"
                 @click="deviceDraftIndex = idx"
                 class="rounded-lg border p-2 space-y-1.5 transition-all cursor-pointer"
                 :class="deviceDraftIndex === idx ? 'border-[var(--col-primary)]/40 bg-[var(--col-primary)]/[0.04]' : 'border-black/10 bg-white/50 hover:bg-white/80'">
              <div class="flex items-center justify-between text-[10px] font-bold opacity-60">
                <span>设备 {{ idx + 1 }}</span>
                <span v-if="deviceDraftIndex === idx" class="text-[var(--col-primary)]">● 当前选中</span>
              </div>
              <div class="grid grid-cols-2 gap-1.5">
                <input v-model="row.name" placeholder="设备名称" class="morandi-input" />
                <input v-model="row.port" placeholder="COM 端口，如 COM7" class="morandi-input font-mono" />
                <input v-model="row.powerOn" placeholder="PWR-ON，如 0x01 0x02" class="morandi-input font-mono" />
                <input v-model="row.powerOff" placeholder="PWR-OFF，如 0xAA 0x00" class="morandi-input font-mono" />
              </div>
            </div>

            <div v-if="deviceEditorError" class="text-[10px] text-[var(--col-error)] bg-[var(--col-error)]/10 border border-[var(--col-error)]/20 rounded-md px-2 py-1.5">{{ deviceEditorError }}</div>
          </div>

          <div class="px-3 py-2 border-t border-black/5 flex items-center justify-between gap-2 shrink-0">
            <div class="flex gap-1.5">
              <button @click="addDeviceDraft" class="px-2 py-1.5 rounded-md bg-white/70 hover:bg-white border border-black/10 text-[10px] font-bold transition-all">新增</button>
              <button @click="removeDeviceDraft" class="px-2 py-1.5 rounded-md bg-white/70 hover:bg-white border border-black/10 text-[10px] font-bold transition-all">删除</button>
            </div>
            <div class="flex gap-1.5">
              <button @click="cancelDeviceEditor" class="px-3 py-1.5 rounded-md bg-white/70 hover:bg-white border border-black/10 text-[10px] font-bold transition-all">取消</button>
              <button @click="saveDeviceEditor" :disabled="deviceSaving"
                      class="px-3 py-1.5 rounded-md bg-[var(--col-primary)] hover:opacity-90 text-white text-[10px] font-bold transition-all disabled:opacity-50">
                {{ deviceSaving ? '保存中…' : '保存' }}
              </button>
            </div>
          </div>
          <div class="px-3 pb-2 text-[9px] text-[var(--text-sub)] opacity-70 truncate shrink-0" :title="pwrDevPath">配置文件：{{ pwrDevPath || 'pwr_dev.ini（与 exe 同目录）' }}</div>
        </div>
      </div>
    </Transition>

    <!-- TASK：数值编辑弹窗（等待时间 / 延时时间 / 运行次数） -->
    <Transition name="modal-fade">
      <div v-if="numberDialog.show" class="fixed inset-0 z-[60] flex items-center justify-center bg-black/20 backdrop-blur-[2px] transition-all">
        <div class="bg-white/95 rounded-xl shadow-2xl border border-white/50 w-[300px] max-w-[90%] overflow-hidden transform transition-all flex flex-col" @click.stop>
          <div class="h-9 flex items-center justify-between px-3 bg-black/[0.03] border-b border-black/5">
            <span class="text-[11px] font-bold tracking-wide text-[var(--col-primary)]">{{ numberDialog.title }}</span>
            <button @click="cancelNumberDialog" class="text-[var(--text-sub)] hover:text-[var(--text-main)] transition-colors"><svg class="w-3.5 h-3.5" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round"><line x1="18" y1="6" x2="6" y2="18"></line><line x1="6" y1="6" x2="18" y2="18"></line></svg></button>
          </div>
          <div class="p-3 flex items-center gap-2">
            <input v-model.number="numberDialog.value" type="number" :min="numberDialog.min" :max="numberDialog.max"
                   class="morandi-input font-mono flex-1" @keydown.enter.prevent="confirmNumberDialog" />
            <span class="text-[11px] text-[var(--text-sub)] shrink-0">{{ numberDialog.unit }}</span>
          </div>
          <div class="px-3 pb-3 flex justify-end gap-1.5">
            <button @click="cancelNumberDialog" class="px-3 py-1.5 rounded-md bg-white/70 hover:bg-white border border-black/10 text-[10px] font-bold transition-all">取消</button>
            <button @click="confirmNumberDialog" class="px-3 py-1.5 rounded-md bg-[var(--col-primary)] hover:opacity-90 text-white text-[10px] font-bold transition-all">保存</button>
          </div>
        </div>
      </div>
    </Transition>

    <!-- AUTO CMD：命令文本编辑弹窗 -->
    <Transition name="modal-fade">
      <div v-if="autoCmdDialog.show" class="fixed inset-0 z-[60] flex items-center justify-center bg-black/20 backdrop-blur-[2px] transition-all">
        <div class="bg-white/95 rounded-xl shadow-2xl border border-white/50 w-[460px] max-w-[90%] overflow-hidden transform transition-all flex flex-col" @click.stop>
          <div class="h-9 flex items-center justify-between px-3 bg-black/[0.03] border-b border-black/5">
            <span class="text-[11px] font-bold tracking-wide text-[var(--col-primary)]">
              {{ autoCmdDialog.index >= 0 ? `编辑命令 ${autoCmdDialog.index + 1}` : '编辑命令' }}
            </span>
            <button @click="cancelAutoCmdDialog" class="text-[var(--text-sub)] hover:text-[var(--text-main)] transition-colors"><svg class="w-3.5 h-3.5" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round"><line x1="18" y1="6" x2="6" y2="18"></line><line x1="6" y1="6" x2="18" y2="18"></line></svg></button>
          </div>
          <div class="p-3">
            <div class="flex items-start gap-2">
              <!-- 点击标志在 TX / RX 之间切换：TX 绿色，RX 红色 -->
              <button @click="toggleDialogCmdType"
                      class="w-10 h-8 shrink-0 rounded-md border text-[10px] font-bold tracking-wider transition-all active:scale-95"
                      :class="autoCmdDialog.type === 'RX'
                        ? 'text-[#b3564e] border-[#b3564e]/30 bg-[#b3564e]/[0.07] hover:bg-[#b3564e]/[0.14]'
                        : 'text-[#3f7d5b] border-[#3f7d5b]/30 bg-[#3f7d5b]/[0.07] hover:bg-[#3f7d5b]/[0.14]'"
                      :title="`点击切换为 ${autoCmdDialog.type === 'TX' ? 'RX' : 'TX'}`">
                {{ autoCmdDialog.type }}
              </button>
              <textarea v-model="autoCmdDialog.text" rows="3" ref="autoCmdInputRef"
                        class="flex-1 min-w-0 morandi-input font-mono resize-none leading-5"
                        placeholder="输入一条命令，例如 AT+RST"
                        @keydown.ctrl.enter.prevent="confirmAutoCmdDialog"
                        @keydown.enter.stop></textarea>
              <!-- 行尾数字：与命令一起在这里编辑，固定两位（00 ~ 99），超过两位提示有误 -->
              <div class="w-[64px] shrink-0 flex flex-col items-stretch gap-1">
                <input ref="autoCmdNumberRef"
                       :value="autoCmdDialog.numberText"
                       type="text" inputmode="numeric" maxlength="2"
                       class="morandi-input font-mono w-full h-8 text-center tabular-nums"
                       :class="autoCmdDialog.numberError ? 'border-[var(--col-error)] ring-1 ring-[var(--col-error)]/40' : ''"
                       placeholder="00"
                       title="行尾数字，两位（00 ~ 99）"
                       @input="onCmdNumberInput"
                       @keydown.enter.prevent="onCmdNumberEnter" />
                <span class="text-[9px] leading-3 text-center text-[var(--text-sub)]">数字</span>
              </div>
            </div>
            <p v-if="autoCmdDialog.numberError" class="mt-1 text-[10px] text-[var(--col-error)]">{{ autoCmdDialog.numberError }}</p>
            <!-- 功能键插入条：点一下就把标记插到光标处，发送时由后端换成实际按键字节 -->
            <div class="mt-2 flex flex-wrap items-center gap-1">
              <span class="text-[9px] text-[var(--text-sub)] mr-0.5">功能键</span>
              <button v-for="k in cmdKeyButtons" :key="k.token" type="button"
                      @click="insertCmdKey(k.token)"
                      class="px-1.5 h-5 rounded border border-black/10 bg-white/70 hover:bg-white text-[10px] font-mono text-[var(--text-main)] transition-all active:scale-95"
                      :title="k.tip">{{ k.label }}</button>
            </div>
            <p class="mt-1 text-[10px] text-[var(--text-sub)]">点左侧标志可在 TX / RX 之间切换；数字只能两位（00 ~ 99）；Ctrl + Enter 保存；清空内容后保存会删除这一条。</p>
            <p class="mt-0.5 text-[10px] leading-4 text-[var(--text-sub)]">功能键标记会原样保存在命令里（如 <span class="font-mono">AT+RST{ENTER}{UP}</span>），发送时替换成实际按键；末尾没有 {ENTER}/{LF} 时 TX 会自动补一个回车；{0x1B} 可发任意单字节。</p>
          </div>
          <div class="px-3 pb-3 flex justify-end gap-1.5">
            <button @click="cancelAutoCmdDialog" class="px-3 py-1.5 rounded-md bg-white/70 hover:bg-white border border-black/10 text-[10px] font-bold transition-all">取消</button>
            <button @click="confirmAutoCmdDialog" class="px-3 py-1.5 rounded-md bg-[var(--col-primary)] hover:opacity-90 text-white text-[10px] font-bold transition-all">保存</button>
          </div>
        </div>
      </div>
    </Transition>

    <!-- 自定义弹窗 (Modal) -->
    <Transition name="modal-fade">
      <div v-if="modal.show" class="fixed inset-0 z-50 flex items-center justify-center bg-black/20 backdrop-blur-[2px] transition-all">
        <div class="bg-white/95 rounded-xl shadow-2xl border border-white/50 w-[420px] max-w-[90%] overflow-hidden transform transition-all scale-100 flex flex-col" @click.stop>
          <div class="h-10 flex items-center justify-between px-4 bg-black/[0.03] border-b border-black/5">
            <div class="flex items-center gap-2">
              <svg v-if="modal.type === 'error'" class="w-4 h-4 text-[var(--col-error)]" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2.5" stroke-linecap="round" stroke-linejoin="round"><circle cx="12" cy="12" r="10"></circle><line x1="12" y1="8" x2="12" y2="12"></line><line x1="12" y1="16" x2="12.01" y2="16"></line></svg>
              <svg v-else class="w-4 h-4 text-[var(--col-primary)]" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2.5" stroke-linecap="round" stroke-linejoin="round"><circle cx="12" cy="12" r="10"></circle><line x1="12" y1="16" x2="12" y2="12"></line><line x1="12" y1="8" x2="12.01" y2="8"></line></svg>
              <span class="text-xs font-bold tracking-wide" :class="modal.type === 'error' ? 'text-[var(--col-error)]' : 'text-[var(--col-primary)]'">{{ modal.title }}</span>
            </div>
            <button @click="closeModal" class="text-[var(--text-sub)] hover:text-[var(--text-main)] transition-colors"><svg class="w-4 h-4" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round"><line x1="18" y1="6" x2="6" y2="18"></line><line x1="6" y1="6" x2="18" y2="18"></line></svg></button>
          </div>
          <div class="p-5"><p class="text-sm text-[var(--text-main)] leading-relaxed font-medium mb-1 opacity-90 break-words font-mono bg-black/5 p-3 rounded-lg border border-black/5 text-[11px] max-h-40 overflow-y-auto custom-scrollbar">{{ modal.message }}</p></div>
          <div class="px-5 pb-5 flex justify-end"><button @click="closeModal" class="bg-[var(--col-primary)] text-white text-xs font-bold px-6 py-2 rounded-lg hover:opacity-90 active:scale-95 transition-all shadow-sm">确 定</button></div>
        </div>
      </div>
    </Transition>

  </div>
</template>

<style scoped>
.control-group { display: flex; align-items: center; gap: 0.5rem; }
.control-group label { width: 60px; text-align: right; font-size: 0.75rem; color: var(--text-sub); }
.morandi-input { width: 100%; background-color: rgba(255, 255, 255, 0.6); border: 1px solid rgba(0, 0, 0, 0.1); color: var(--text-main); padding: 0.25rem 0.5rem; font-size: 0.8rem; border-radius: 0.375rem; outline: none; transition: all 0.2s; }
.morandi-input:focus { background-color: #fff; border-color: var(--col-primary); }
.morandi-input:disabled { opacity: 0.6; cursor: not-allowed; }
.custom-scrollbar::-webkit-scrollbar { width: 6px; height: 6px; }
.custom-scrollbar::-webkit-scrollbar-track { background: transparent; }
.custom-scrollbar::-webkit-scrollbar-thumb { background: rgba(0,0,0,0.15); border-radius: 3px; }
.custom-scrollbar::-webkit-scrollbar-thumb:hover { background: var(--col-primary); }

.fade-enter-active,
.fade-leave-active { transition: opacity 0.2s ease, transform 0.2s ease; }
.fade-enter-from,
.fade-leave-to { opacity: 0; transform: translateY(5px); }

.slide-fade-enter-active { transition: all 0.2s ease-out; }
.slide-fade-leave-active { transition: all 0.15s cubic-bezier(1, 0.5, 0.8, 1); }
.slide-fade-enter-from,
.slide-fade-leave-to { transform: translateY(-5px); opacity: 0; }

/* 主题面板下拉动画 */
.slide-down-enter-active { transition: all 0.3s cubic-bezier(0.16, 1, 0.3, 1); }
.slide-down-leave-active { transition: all 0.2s ease-in; }
.slide-down-enter-from,
.slide-down-leave-to { transform: translateY(-10px); opacity: 0; }

/* 背景遮罩淡入淡出动画 */
.backdrop-fade-enter-active { transition: opacity 0.25s ease-out; }
.backdrop-fade-leave-active { transition: opacity 0.2s ease-in; }
.backdrop-fade-enter-from,
.backdrop-fade-leave-to { opacity: 0; }

/* 汉堡菜单下拉动画 */
.dropdown-fade-enter-active { transition: all 0.2s cubic-bezier(0.16, 1, 0.3, 1); }
.dropdown-fade-leave-active { transition: all 0.15s ease-in; }
.dropdown-fade-enter-from,
.dropdown-fade-leave-to { transform: scale(0.95) translateY(-5px); opacity: 0; }

.modal-fade-enter-active,
.modal-fade-leave-active { transition: all 0.2s ease-out; }
.modal-fade-enter-from,
.modal-fade-leave-to { opacity: 0; }
.modal-fade-enter-from .bg-white\/95,
.modal-fade-leave-to .bg-white\/95 { transform: scale(0.95); opacity: 0; }

@keyframes shake-x {
  0%, 100% { transform: translateX(0); }
  10%, 30%, 50%, 70%, 90% { transform: translateX(-4px); }
  20%, 40%, 60%, 80% { transform: translateX(4px); }
}
.shake-anim { animation: shake-x 0.4s cubic-bezier(0.36, 0.07, 0.19, 0.97) both;}

/* 新增：利用 CSS 选择器，当父级震动时，直接让内部带边框的元素变红 */
.shake-anim :deep(.border) {
  /* 强制边框变红 */
  border-color: var(--col-error) !important;
  /* 添加一道 1px 的无模糊红色外扩实线，增强视觉厚度，比单纯变色更明显 */
  box-shadow: 0 0 0 0.1px var(--col-error) !important;
  /* 稍微加一点过渡，让红色褪去时平滑一些（出现时是瞬时的） */
  transition: border-color 0.1s, box-shadow 0.1s;
}

@keyframes broom-sweep {
  0%, 100% { transform: rotate(0deg) translateX(0); }
  25% { transform: rotate(-8deg) translateX(-2px); }
  50% { transform: rotate(8deg) translateX(2px); }
  75% { transform: rotate(-8deg) translateX(-2px); }
}
.broom-icon {
  transition: transform 0.2s ease;
}
.group:hover .broom-icon {
  animation: broom-sweep 0.6s ease-in-out;
}
.broom-icon.broom-clicked {
  animation: broom-sweep 0.6s ease-in-out;
}

/* AUTO CMD 底部按钮的外框：
   - 停止：外圈呼吸灯（缓慢明暗 + 柔和外发光）
   - 运行：SVG 描边 + pathLength 归一化，沿整圈边框按弧长匀速流动，长边短边速度一致 */
.run-ring {
  position: relative;
  padding: 3px;
  border-radius: 12px;
  transition: background-color 0.3s ease, box-shadow 0.3s ease;
}
.run-ring--idle {
  background: rgba(122, 148, 160, 0.28);
  animation: breath-ring-glow 2.6s ease-in-out infinite;
}
.run-ring--running {
  background: rgba(255, 255, 255, 0.55);
  box-shadow: 0 0 10px rgba(122, 148, 160, 0.45);
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
.run-ring__dash {
  animation: run-ring-flow 2.4s linear infinite;
}
@keyframes run-ring-flow {
  from { stroke-dashoffset: 100; }
  to { stroke-dashoffset: 0; }
}
@keyframes breath-ring-glow {
  0%, 100% {
    background: rgba(122, 148, 160, 0.28);
    box-shadow: 0 0 0 0 rgba(122, 148, 160, 0);
  }
  50% {
    background: rgba(122, 148, 160, 0.85);
    box-shadow: 0 0 12px 2px rgba(122, 148, 160, 0.45);
  }
}
</style>