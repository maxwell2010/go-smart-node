"""Read-only Linux resource samples for processes owned by a disposable CI lab."""

import os
from pathlib import Path
import stat


def proc_fields(path):
    fields = {}
    for line in path.read_text().splitlines():
        name, separator, value = line.partition(':')
        if separator:
            fields[name] = value.strip()
    return fields


def proc_kib(fields, name):
    count, unit = fields[name].split()
    if unit != 'kB' or int(count) < 0:
        raise ValueError(f'invalid Linux {name} unit or value')
    return int(count) * 1024


def runner_resources():
    return {'cpuAffinityCount': len(os.sched_getaffinity(0)),
            'memoryTotalBytes': proc_kib(proc_fields(Path('/proc/meminfo')), 'MemTotal'),
            'kernelRelease': os.uname().release}


def allocated_data_bytes(directory):
    if not directory.is_dir():
        raise ValueError('disposable node data directory is absent')
    total = 0
    for parent, _, files in os.walk(directory):
        for name in files:
            try:
                sample = (Path(parent) / name).lstat()
            except FileNotFoundError:
                # LevelDB compaction may unlink a file after enumeration.
                continue
            if stat.S_ISREG(sample.st_mode):
                total += sample.st_blocks * 512
    return total


def process_usage(pid, data_directory=None):
    if type(pid) is not int or pid <= 1:
        raise ValueError('invalid owned process ID')
    proc = Path('/proc') / str(pid)
    status = proc_fields(proc / 'status')
    # comm may contain spaces and parentheses. The first field after it is
    # state (stat field 3), followed by the fixed numeric fields.
    counters = (proc / 'stat').read_text().rsplit(') ', 1)[1].split()
    ticks = os.sysconf('SC_CLK_TCK')
    uptime = float(Path('/proc/uptime').read_text().split()[0])
    peak = proc_kib(status, 'VmHWM')
    if peak == 0 or counters[0] in ('Z', 'X'):
        raise ValueError('expected a live Linux process with resident memory')
    sample = {'peakResidentBytes': peak, 'residentBytes': proc_kib(status, 'VmRSS'),
              'cpuSeconds': round((int(counters[11]) + int(counters[12])) / ticks, 3),
              'processAgeSeconds': round(max(0, uptime - int(counters[19]) / ticks), 3)}
    if data_directory is not None:
        sample['dataAllocatedBytes'] = allocated_data_bytes(data_directory)
    return sample


def replica_usage(replica):
    if replica.child.poll() is not None:
        raise ValueError('cannot measure a stopped CI replica')
    return process_usage(replica.child.pid, replica.home / 'data')
