import AppKit
import Foundation

final class AppDelegate: NSObject, NSApplicationDelegate {
    private var worker: Process?
    private var logHandle: FileHandle?
    private var signalSources: [DispatchSourceSignal] = []

    func applicationDidFinishLaunching(_ notification: Notification) {
        for signalNumber in [SIGTERM, SIGINT] {
            signal(signalNumber, SIG_IGN)
            let source = DispatchSource.makeSignalSource(signal: signalNumber, queue: .main)
            source.setEventHandler {
                NSApplication.shared.terminate(nil)
            }
            source.resume()
            signalSources.append(source)
        }

        let logPath = "/Users/curtishays/Library/Logs/VideoDropConverter/video-drop-converter.log"
        FileManager.default.createFile(atPath: logPath, contents: nil)
        logHandle = FileHandle(forWritingAtPath: logPath)
        logHandle?.seekToEndOfFile()

        let process = Process()
        process.executableURL = URL(fileURLWithPath: "/usr/bin/python3")
        process.arguments = [
            "/Users/curtishays/Library/Application Support/VideoDropConverter/video_drop_converter.py",
            "--watch-dir", "/Volumes/Video2/Convert to MP4",
            "--processed-dir", "/Volumes/Video2/Processed"
        ]
        process.standardOutput = logHandle
        process.standardError = logHandle
        process.terminationHandler = { _ in
            DispatchQueue.main.async {
                NSApplication.shared.terminate(nil)
            }
        }

        do {
            try process.run()
            worker = process
        } catch {
            let message = "Could not start video converter: \(error)\n"
            logHandle?.write(message.data(using: .utf8)!)
            NSApplication.shared.terminate(nil)
        }
    }

    func applicationWillTerminate(_ notification: Notification) {
        if let worker, worker.isRunning {
            worker.terminate()
            worker.waitUntilExit()
        }
        try? logHandle?.close()
    }
}

let app = NSApplication.shared
let delegate = AppDelegate()
app.delegate = delegate
app.setActivationPolicy(.accessory)
app.run()
