# tn-cli Refactoring Summary

The code is organized into following focused modules:

## New Structure

### 1. **tn-cli.py** (Entry Point - ~120 lines)
- Command-line argument parsing
- Application initialization
- Version handling
- Authentication setup (token, basic, cookie)
- Macro loading
- Entry point that calls `run()` from client module

**Key functions:**
- `exception_hook()` - Crash handler
- `if __name__ == '__main__'` - Main entry point

---

### 2. **utils.py** (Utility Functions - ~200 lines)
- Helper functions and data structures
- File/image processing utilities
- Encoding and parsing functions

**Key functions:**
- `dotdict` - Dictionary with dot notation access
- `makeTheCard()` - Pack user profile data
- `inline_image()` - Create drafty image messages
- `attachment()` - Create drafty attachment messages
- `encode_to_bytes()` - Convert objects to bytes
- `parse_cred()` - Parse credentials
- `parse_trusted()` - Parse trusted values

**Constants:**
- `MAX_INBAND_ATTACHMENT_SIZE`
- `MAX_EXTERN_ATTACHMENT_SIZE`
- `MAX_IMAGE_DIM`
- `DELETE_MARKER`
- `TINODE_DEL`

---

### 3. **commands.py** (Command Parsing & Message Building - ~850 lines)
- Command-line parsing for all commands
- Protobuf message construction
- Variable dereferencing
- Command serialization

**Key functions:**
- `parse_input()` - Parse command line input
- `parse_cmd()` - Create argument parsers
- `serialize_cmd()` - Convert commands to protobuf
- `derefVals()` / `getVar()` - Variable dereferencing
- Message builders: `hiMsg()`, `accMsg()`, `loginMsg()`, `subMsg()`, `leaveMsg()`, `pubMsg()`, `getMsg()`, `setMsg()`, `delMsg()`, `noteMsg()`
- File operations: `upload()`, `fileUpload()`, `fileDownload()`
- `print_server_params()` - Log server info

---

### 4. **client.py** (gRPC Client & Communication - ~260 lines)
- gRPC connection management
- Message generation and streaming
- Server response handling
- Login/authentication handling
- Cookie management

**Key functions:**
- `run()` - Main client loop
- `gen_message()` - Generate outgoing messages
- `handle_ctrl()` - Handle server control responses
- `handle_login()` - Process login response
- `save_cookie()` / `read_cookie()` - Cookie persistence
- `pop_from_output_queue()` - Output queue management

---

### 5. **input_handler.py** (User Input - ~70 lines)
- Terminal input reading
- Multi-line input support
- Interactive and non-interactive modes

**Key functions:**
- `stdin()` - Main input loop
- `readLinesFromStdin()` - Read with prompt support

---

### 6. **tn_globals.py** (Shared Global State - ~104 lines)
- Global variables shared across all modules
- Asynchronous I/O queue management
- Utility functions for logging and output
- Protobuf to JSON conversion

**Key variables:**
- `OnCompletion` - Dictionary of callbacks for server responses
- `WaitingFor` - Outstanding synchronous command request
- `AuthToken` - Current authentication token
- `InputQueue` / `OutputQueue` - Async I/O queues
- `InputThread` - Background input thread
- `IsInteractive` - Detect if running in interactive mode
- `Prompt` - PromptSession for interactive input
- `DefaultUser` / `DefaultTopic` - Default context values
- `Variables` - Store command execution results
- `Connection` - gRPC connection to server
- `Verbose` - Extended logging flag

**Key functions:**
- `printout()` - Print in interactive mode only
- `printerr()` - Write to stderr
- `stdout()` / `stdoutln()` - Async output to stdout
- `clip_long_string()` - Shorten long strings for logging
- `to_json()` - Convert protobuf messages to JSON

---

### 7. **macros.py** (Command Macros - ~341 lines)
- High-level command macros that expand into basic commands
- Simplifies complex multi-step operations
- Requires root privileges for most operations

**Macro base class:**
- `Macro` - Base class for all macros with parsing and execution

**Available macros:**
- `usermod` - Modify user account (suspend/unsuspend, update theCard, trusted values)
- `resolve` - Resolve login name to user ID
- `passwd` - Set user's password
- `useradd` - Create new user account with credentials
- `chacs` - Change default permissions/acs for a user
- `userdel` - Delete user account (soft or hard delete)
- `chcred` - Add/delete/validate user credentials
- `thecard` - Print user's public/private data or credentials

**Key functions:**
- `parse_macro()` - Find parser for macro command
- `Macro.expand()` - Expand macro to list of basic commands
- `Macro.run()` - Execute macro or explain expansion

**Macro dictionary:**
- `Macros` - Dictionary mapping macro names to instances

---

## Module Dependencies

```
tn-cli.py
├── tn_globals
├── client (run, read_cookie)
└── commands (set_macros_module)

client.py
├── tn_globals
├── tinode_grpc (pb, pbx)
├── utils (dotdict)
├── input_handler (stdin)
└── commands (hiMsg, loginMsg, serialize_cmd)

commands.py
├── tn_globals
├── tinode_grpc (pb, pbx)
├── utils (makeTheCard, inline_image, attachment, etc.)
└── client (handle_ctrl, handle_login, save_cookie) [for specific commands]

utils.py
├── tn_globals
└── tinode_grpc (pb)

input_handler.py
└── tn_globals

macros.py
└── tn_globals

tn_globals.py
└── (no dependencies - provides shared state)
```

## Why

1. **Separation of Concerns**: Each module has a clear, focused responsibility
2. **Maintainability**: Easier to find and modify specific functionality
3. **Testability**: Individual modules can be tested independently
4. **Readability**: Smaller files are easier to understand
5. **Reusability**: Utilities and client code can be reused
6. **Extensibility**: Easy to add new macros or commands without modifying core logic
7. **Shared State Management**: `tn_globals.py` provides centralized state accessible to all modules

## Usage

Run the application using:
```bash
python3 tn-cli.py [arguments]
```

Or make it executable:
```bash
chmod +x tn-cli.py
./tn-cli.py [arguments]
```
