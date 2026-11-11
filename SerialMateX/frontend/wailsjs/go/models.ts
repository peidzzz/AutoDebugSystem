export namespace main {
	
	export class AutoCmd {
	    type: string;
	    number: number;
	    text: string;
	
	    static createFrom(source: any = {}) {
	        return new AutoCmd(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.type = source["type"];
	        this.number = source["number"];
	        this.text = source["text"];
	    }
	}
	export class PwrDevice {
	    name: string;
	    port: string;
	    powerOn: string;
	    powerOff: string;
	
	    static createFrom(source: any = {}) {
	        return new PwrDevice(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.name = source["name"];
	        this.port = source["port"];
	        this.powerOn = source["powerOn"];
	        this.powerOff = source["powerOff"];
	    }
	}
	export class TaskConfig {
	    saveLog: boolean;
	    savePower: boolean;
	    initialState: boolean;
	    abnormalPause: boolean;
	    waitSeconds: number;
	    delaySeconds: number;
	    runCount: number;
	
	    static createFrom(source: any = {}) {
	        return new TaskConfig(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.saveLog = source["saveLog"];
	        this.savePower = source["savePower"];
	        this.initialState = source["initialState"];
	        this.abnormalPause = source["abnormalPause"];
	        this.waitSeconds = source["waitSeconds"];
	        this.delaySeconds = source["delaySeconds"];
	        this.runCount = source["runCount"];
	    }
	}
	export class TaskRunOptions {
	    deviceName: string;
	    eol: string;
	
	    static createFrom(source: any = {}) {
	        return new TaskRunOptions(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.deviceName = source["deviceName"];
	        this.eol = source["eol"];
	    }
	}
	export class TaskRuntime {
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
	
	    static createFrom(source: any = {}) {
	        return new TaskRuntime(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.running = source["running"];
	        this.paused = source["paused"];
	        this.round = source["round"];
	        this.runCount = source["runCount"];
	        this.normalCount = source["normalCount"];
	        this.powerOn = source["powerOn"];
	        this.current = source["current"];
	        this.phase = source["phase"];
	        this.countdown = source["countdown"];
	        this.countdownTo = source["countdownTo"];
	        this.cmdIndex = source["cmdIndex"];
	        this.cmdPhase = source["cmdPhase"];
	        this.cmdCountdown = source["cmdCountdown"];
	        this.logPath = source["logPath"];
	        this.csvPath = source["csvPath"];
	        this.message = source["message"];
	    }
	}

}

export namespace time {
	
	export class Time {
	
	
	    static createFrom(source: any = {}) {
	        return new Time(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	
	    }
	}

}

export namespace updater {
	
	export class UpdateInfo {
	    available: boolean;
	    currentVersion: string;
	    latestVersion: string;
	    releaseNotes: string;
	    downloadUrl: string;
	    assetSize: number;
	
	    static createFrom(source: any = {}) {
	        return new UpdateInfo(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.available = source["available"];
	        this.currentVersion = source["currentVersion"];
	        this.latestVersion = source["latestVersion"];
	        this.releaseNotes = source["releaseNotes"];
	        this.downloadUrl = source["downloadUrl"];
	        this.assetSize = source["assetSize"];
	    }
	}

}

