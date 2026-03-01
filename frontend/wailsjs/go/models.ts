export namespace main {
	
	export class AudioDevice {
	    id: string;
	    name: string;
	
	    static createFrom(source: any = {}) {
	        return new AudioDevice(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.id = source["id"];
	        this.name = source["name"];
	    }
	}
	export class AudioItem {
	    id: string;
	    name: string;
	    path: string;
	    hotkey: string;
	    duration: string;
	    size: string;
	
	    static createFrom(source: any = {}) {
	        return new AudioItem(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.id = source["id"];
	        this.name = source["name"];
	        this.path = source["path"];
	        this.hotkey = source["hotkey"];
	        this.duration = source["duration"];
	        this.size = source["size"];
	    }
	}
	export class CheckUpdateResult {
	    has_update: boolean;
	    latest_version: string;
	    download_url: string;
	    error: string;
	
	    static createFrom(source: any = {}) {
	        return new CheckUpdateResult(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.has_update = source["has_update"];
	        this.latest_version = source["latest_version"];
	        this.download_url = source["download_url"];
	        this.error = source["error"];
	    }
	}
	export class Config {
	    audio_list: AudioItem[];
	    close_action: string;
	    dont_ask_again: boolean;
	    volume: number;
	    main_device: string;
	    aux_device: string;
	    window_width: number;
	    window_height: number;
	    sidebar_collapsed: boolean;
	
	    static createFrom(source: any = {}) {
	        return new Config(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.audio_list = this.convertValues(source["audio_list"], AudioItem);
	        this.close_action = source["close_action"];
	        this.dont_ask_again = source["dont_ask_again"];
	        this.volume = source["volume"];
	        this.main_device = source["main_device"];
	        this.aux_device = source["aux_device"];
	        this.window_width = source["window_width"];
	        this.window_height = source["window_height"];
	        this.sidebar_collapsed = source["sidebar_collapsed"];
	    }
	
		convertValues(a: any, classs: any, asMap: boolean = false): any {
		    if (!a) {
		        return a;
		    }
		    if (a.slice && a.map) {
		        return (a as any[]).map(elem => this.convertValues(elem, classs));
		    } else if ("object" === typeof a) {
		        if (asMap) {
		            for (const key of Object.keys(a)) {
		                a[key] = new classs(a[key]);
		            }
		            return a;
		        }
		        return new classs(a);
		    }
		    return a;
		}
	}

}

