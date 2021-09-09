git checkout go.mod
git checkout go.sum
git pull origin master
go mod tidy
go mod vendor