const express = require('express');
const { spawn } = require('child_process');

const app = express();
app.use(express.json());
const port = process.argv[2] || 3000;
const ip = process.argv[3] || '0.0.0.0';
const tinode_grpc_server = process.argv[4] || 'localhost:16060'; // 34.101.45.102:16060
const rootAccount = process.env.TINODE_ROOT_ACCOUNT_CRED || 'bob:bob123';
const pythonBinary = process.env.PYTHON_BINARY || 'python';

app.post('/execute', async (req, res) => {
    console.log(req.body)
    // Request Body: {"script": "chatdemo --what cred --group grppQw179RgniM usrMSeBdwoBrJ8,usrMDDxrn4YuLg,usrHKiDc0XQudY\n"}
    const tinode_script = req.body.script;
    // bob is ROOT. How? See tinode-db/README.md
    let pythonProcess = spawn(pythonBinary, ['tn-cli.py', '--host', tinode_grpc_server, '--verbose', '--login-basic', rootAccount]);

    pythonProcess.stdin.write(tinode_script);
    pythonProcess.stdin.end();

    pythonProcess.stdout.on('data', (data) => {
        console.log(`stdout: ${data}`);
    });

    pythonProcess.stderr.on('data', (data) => {
        console.error(`stderr: ${data}`);
    });

    pythonProcess.on('close', (code) => {
        console.log(`child process exited with code ${code}`);
        res.send(`child process exited with code ${code}`);
    });
});

app.post('/create/group-chat', (req, res) => {
    res.setHeader('Content-Type', 'application/json');
    let groupName = req.body.name;
    let groupOwnnerId = req.body.group_admin;
    let userids = req.body.userids.join(",");

    let pythonProcess = spawn(pythonBinary, ['tn-cli-json.py', '--host', tinode_grpc_server, '--verbose', '--login-basic', rootAccount]);
    let pythonProcess2 = spawn(pythonBinary, ['tn-cli-json.py', '--host', tinode_grpc_server, '--verbose', '--login-basic', rootAccount]);
    pythonProcess.stdin.write("crgroup '"+ groupOwnnerId +"' --name '" + groupName + "'");
    pythonProcess.stdin.end();

    var groupData = {};
    pythonProcess.stdout.on('data', (data) => {
        var str = data.toString(), lines = str.split(/(\r?\n)/g);
        for (var i = 0; i < lines.length; i++) {
            try {
                let resp = JSON.parse(lines[i])

                if (resp.in.ctrl) {
                    if (resp.in.ctrl.topic.includes('grp')) {
                        groupData = resp.in.ctrl;

                        pythonProcess2.stdin.write("substogroup '" + userids + "' --groupid '" + groupData.topic + "'");
                        pythonProcess2.stdin.end();
                    }
                }
            } catch (e) {
            }
        }
    });

    pythonProcess.stderr.on('data', (data) => {
        console.error(`stderr: ${data}`);
    });

    pythonProcess.on('close', (code) => {
        console.log(`child process exited with code ${code}`);
        res.json(groupData)
    });
});

app.listen(port, ip, () => {
    console.log(`Server is running on port ${port}`);
});