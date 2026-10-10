#!/usr/bin/perl
# kmod.pl extract <sandbox-api binary> <out.ko>   - pull the embedded virtio_ring_resync.ko out of sandbox-api
# kmod.pl load <file.ko> [param=value ...]         - finit_module with vermagic/modversions ignored (like sandbox-api does)
# kmod.pl unload
use strict; use warnings;
my ($cmd, @a) = @ARGV;
if ($cmd eq 'extract') {
    my ($bin, $out) = @a;
    open my $f, '<:raw', $bin or die "$bin: $!";
    local $/; my $d = <$f>; close $f;
    # ET_REL x86_64 ELF header (the module); sandbox-api itself is ET_DYN.
    my $pos = 0; my $found;
    while (($pos = index($d, "\x7fELF\x02\x01\x01\x00", $pos)) >= 0) {
        if (unpack('v', substr($d, $pos + 16, 2)) == 1 && unpack('v', substr($d, $pos + 18, 2)) == 62) { $found = $pos; last }
        $pos++;
    }
    die "no embedded module in $bin (built without make build-kmod?)\n" unless defined $found;
    my $shoff = unpack('Q<', substr($d, $found + 0x28, 8));
    my $shentsize = unpack('v', substr($d, $found + 0x3a, 2));
    my $shnum = unpack('v', substr($d, $found + 0x3c, 2));
    my $len = $shoff + $shentsize * $shnum;
    open my $o, '>:raw', $out or die "$out: $!"; print $o substr($d, $found, $len); close $o;
    print "wrote $out ($len bytes)\n";
} elsif ($cmd eq 'load') {
    my ($ko, @params) = @a;
    open my $f, '<:raw', $ko or die "$ko: $!";
    my $params = join(' ', @params);
    my $r = syscall(313, fileno($f), $params, 3);  # finit_module, MODULE_INIT_IGNORE_MODVERSIONS|IGNORE_VERMAGIC
    print $r == 0 ? "finit_module: ok\n" : "finit_module: $!\n";
    if (open my $p, '<', '/sys/module/virtio_ring_resync/parameters/resynced') { print "resynced: ", <$p> }
} elsif ($cmd eq 'unload') {
    my $n = "virtio_ring_resync"; my $r = syscall(176, $n, 0);  # delete_module
    print $r == 0 ? "delete_module: ok\n" : "delete_module: $!\n";
} else { die "usage: kmod.pl extract|load|unload\n" }
