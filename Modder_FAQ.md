# Republic Mod Loader — Modder DLL FAQ

## What does my mod need for RML to discover its DLL?

Place the DLL inside a folder named `plugin` or `plugins` in your Workshop item. RML checks for those folders at the Workshop item root and one folder level below it. A separate RML manifest is **not required** for discovery; a valid `.dll` can be discovered and listed on its own.

## What makes the DLL valid for RML to load?

The file must be a readable **64-bit Windows DLL**: a valid PE image, declared as a DLL (`IMAGE_FILE_DLL`), built for **AMD64 / x86-64**, and using the **PE32+** format. Those are RML's static admission requirements. RML-specific metadata/API exports are optional. If RML detects a supported plugin lifecycle it will use it; otherwise a structurally valid DLL can still be attempted through normal Windows `LoadLibrary` behaviour.
