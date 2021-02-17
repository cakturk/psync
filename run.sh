#!/bin/sh
set -eu

srv="/tmp/t/srv"
cli="/tmp/t/cli"

mk_rm_emptydir() {
	mkdir -p "$cli/dir0"
	rmdir "$cli/dir0"
}
creat_modify_rm_file() {
	cat <<-EOF > "$cli/newfile0"
	random content,
	damla
	EOF
	cat <<-EOF >> "$cli/newfile0"
	appending some text
	EOF
	rm "$cli/newfile0"
}
summary() { rsync -rcnv "$cli/" "$srv/"; }

creat_modify_rm_file
# mk_rm_emptydir
summary
